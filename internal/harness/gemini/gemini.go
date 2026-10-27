package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness"
)

type Adapter struct {
	binaryPath string
}

func NewAdapter() *Adapter {
	return &Adapter{
		binaryPath: "gemini",
	}
}

func (a *Adapter) Name() string {
	return "gemini"
}

func (a *Adapter) Check(ctx context.Context) (harness.VersionInfo, harness.AuthStatus, error) {
	var ver harness.VersionInfo
	var auth harness.AuthStatus

	// 1. Version check
	cmdVer := exec.CommandContext(ctx, a.binaryPath, "--version")
	outVer, err := cmdVer.Output()
	if err != nil {
		return ver, auth, fmt.Errorf("failed to run gemini --version: %w", err)
	}
	ver.Raw = strings.TrimSpace(string(outVer))
	parts := strings.Fields(ver.Raw)
	if len(parts) > 0 {
		ver.Version = parts[len(parts)-1]
	}

	// 2. Auth check: GEMINI_API_KEY env or auth command
	if os.Getenv("GEMINI_API_KEY") != "" {
		auth.LoggedIn = true
		auth.AuthMethod = "api_key"
	} else {
		cmdAuth := exec.CommandContext(ctx, a.binaryPath, "auth", "status", "--json")
		outAuth, err := cmdAuth.Output()
		if err == nil {
			var statusRaw struct {
				LoggedIn bool   `json:"logged_in"`
				Email    string `json:"email"`
			}
			if jsonErr := json.Unmarshal(outAuth, &statusRaw); jsonErr == nil {
				auth.LoggedIn = statusRaw.LoggedIn
				auth.Email = statusRaw.Email
				auth.AuthMethod = "google_oauth"
			}
		}
	}

	return ver, auth, nil
}

func (a *Adapter) StartCommand(spec agent.AgentSpec) []string {
	if spec.Mode == "interactive" {
		cmd := []string{a.binaryPath}
		if spec.Model != "" {
			cmd = append(cmd, "--model", spec.Model)
		}
		if spec.Prompt != "" {
			cmd = append(cmd, spec.Prompt)
		}
		return cmd
	}

	// Task mode: uses gemini -p with stream-json
	cmd := []string{a.binaryPath, "-p"}
	if spec.Prompt != "" {
		cmd = append(cmd, spec.Prompt)
	} else {
		cmd = append(cmd, "")
	}
	cmd = append(cmd, "--output-format", "stream-json")
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	return cmd
}

func (a *Adapter) ResumeCommand(spec agent.AgentSpec, sessionID string) []string {
	if spec.Mode == "interactive" {
		cmd := []string{a.binaryPath, "--resume", sessionID}
		if spec.Model != "" {
			cmd = append(cmd, "--model", spec.Model)
		}
		return cmd
	}

	// Task mode: gemini -p [PROMPT] --output-format stream-json --resume [SESSION_ID]
	cmd := []string{a.binaryPath, "-p"}
	if spec.Prompt != "" {
		cmd = append(cmd, spec.Prompt)
	} else {
		cmd = append(cmd, "")
	}
	cmd = append(cmd, "--output-format", "stream-json", "--resume", sessionID)
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	return cmd
}

func (a *Adapter) Respond(approved bool) []byte {
	if approved {
		return []byte("y\n")
	}
	return []byte("n\n")
}

func (a *Adapter) ParseEvents(ctx context.Context, r io.Reader) (<-chan events.NormalizedEvent, <-chan error) {
	outCh := make(chan events.NormalizedEvent, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(outCh)
		defer close(errCh)

		scanner := bufio.NewScanner(r)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 10*1024*1024)

		var seq int64

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}

			var raw map[string]any
			if err := json.Unmarshal(line, &raw); err != nil {
				continue
			}

			now := time.Now().UTC()
			evType, _ := raw["type"].(string)

			switch evType {
			case "init", "session_start":
				seq++
				sessionID, _ := raw["session_id"].(string)
				ev := events.NewMessageEvent(seq, now, "system", "Session initialized")
				ev.Payload["session_id"] = sessionID
				if m, ok := raw["model"].(string); ok {
					ev.Payload["model"] = m
				}
				outCh <- ev

			case "content", "message":
				role, _ := raw["role"].(string)
				if role == "" {
					role = "assistant"
				}
				text, _ := raw["text"].(string)
				if text != "" {
					seq++
					outCh <- events.NewMessageEvent(seq, now, role, text)
				}

			case "tool_call":
				callID, _ := raw["call_id"].(string)
				name, _ := raw["name"].(string)
				args := raw["args"]
				seq++
				outCh <- events.NewToolCallEvent(seq, now, callID, name, args)

			case "tool_response", "tool_result":
				callID, _ := raw["call_id"].(string)
				out := raw["output"]
				seq++
				outCh <- events.NewToolResultEvent(seq, now, callID, out, false)

			case "subagent_started":
				subID, _ := raw["subagent_id"].(string)
				subType, _ := raw["subagent_type"].(string)
				prompt, _ := raw["prompt"].(string)
				parentID, _ := raw["parent_agent_id"].(string)
				seq++
				outCh <- events.NewSubagentStartedEvent(seq, now, subID, subType, prompt, parentID)

			case "subagent_finished":
				subID, _ := raw["subagent_id"].(string)
				result, _ := raw["result"].(string)
				success, _ := raw["success"].(bool)
				seq++
				outCh <- events.NewSubagentFinishedEvent(seq, now, subID, result, success)

			case "usage":
				var promptTok, compTok int
				if p, ok := raw["prompt_tokens"].(float64); ok {
					promptTok = int(p)
				}
				if c, ok := raw["completion_tokens"].(float64); ok {
					compTok = int(c)
				}
				seq++
				outCh <- events.NewUsageEvent(seq, now, promptTok, compTok, 0.0, "")

			case "finish", "done":
				summary, _ := raw["summary"].(string)
				if summary == "" {
					summary, _ = raw["result"].(string)
				}
				seq++
				outCh <- events.NewDoneEvent(seq, now, summary, "completed")

			case "error":
				msg, _ := raw["message"].(string)
				seq++
				outCh <- events.NewErrorEvent(seq, now, msg, raw)
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			atomic.AddInt64(&seq, 1)
			errCh <- err
		}
	}()

	return outCh, errCh
}
