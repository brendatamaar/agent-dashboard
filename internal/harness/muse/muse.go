package muse

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
		binaryPath: "muse",
	}
}

func (a *Adapter) Name() string {
	return "muse"
}

func (a *Adapter) Check(ctx context.Context) (harness.VersionInfo, harness.AuthStatus, error) {
	var ver harness.VersionInfo
	var auth harness.AuthStatus

	// 1. Version check
	cmdVer := exec.CommandContext(ctx, a.binaryPath, "--version")
	outVer, err := cmdVer.Output()
	if err != nil {
		return ver, auth, fmt.Errorf("failed to run muse --version: %w", err)
	}
	ver.Raw = strings.TrimSpace(string(outVer))
	fields := strings.Fields(ver.Raw)
	if len(fields) >= 3 && fields[0] == "Muse" && fields[1] == "Code" {
		ver.Version = fields[2]
	} else if len(fields) > 0 {
		ver.Version = fields[len(fields)-1]
	}

	// 2. Auth check: MUSE_API_KEY or META_API_KEY env vars
	if os.Getenv("MUSE_API_KEY") != "" || os.Getenv("META_API_KEY") != "" {
		auth.LoggedIn = true
		auth.AuthMethod = "api_key"
	}

	return ver, auth, nil
}

func (a *Adapter) StartCommand(spec agent.AgentSpec) []string {
	if spec.Mode == "interactive" {
		cmd := []string{a.binaryPath}
		if spec.Model != "" {
			cmd = append(cmd, "--model", spec.Model)
		}
		if spec.AutoApprove {
			cmd = append(cmd, "--disable-approval")
		}
		if spec.Prompt != "" {
			cmd = append(cmd, spec.Prompt)
		}
		return cmd
	}

	// Task mode: uses muse exec --json
	cmd := []string{a.binaryPath, "exec", "--json"}
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	if spec.AutoApprove {
		cmd = append(cmd, "--disable-approval")
	}
	if spec.Prompt != "" {
		cmd = append(cmd, spec.Prompt)
	}
	return cmd
}

func (a *Adapter) ResumeCommand(spec agent.AgentSpec, sessionID string) []string {
	if spec.Mode == "interactive" {
		cmd := []string{a.binaryPath, "resume", sessionID}
		if spec.Model != "" {
			cmd = append(cmd, "--model", spec.Model)
		}
		if spec.AutoApprove {
			cmd = append(cmd, "--disable-approval")
		}
		return cmd
	}

	// Task mode: muse exec --json --session-id [SESSION_ID] [PROMPT]
	cmd := []string{a.binaryPath, "exec", "--json", "--session-id", sessionID}
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	if spec.AutoApprove {
		cmd = append(cmd, "--disable-approval")
	}
	if spec.Prompt != "" {
		cmd = append(cmd, spec.Prompt)
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
			case "session_start", "init":
				seq++
				sessionID, _ := raw["session_id"].(string)
				ev := events.NewMessageEvent(seq, now, "system", "Session initialized")
				ev.Payload["session_id"] = sessionID
				if m, ok := raw["model"].(string); ok {
					ev.Payload["model"] = m
				}
				outCh <- ev

			case "assistant_message", "message":
				text, _ := raw["text"].(string)
				if text != "" {
					seq++
					outCh <- events.NewMessageEvent(seq, now, "assistant", text)
				}

			case "tool_execution", "tool_call":
				toolName, _ := raw["tool"].(string)
				if toolName == "" {
					toolName, _ = raw["name"].(string)
				}
				input := raw["input"]
				if input == nil {
					input = raw["args"]
				}
				seq++
				outCh <- events.NewToolCallEvent(seq, now, fmt.Sprintf("call_%d", seq), toolName, input)

			case "tool_output", "tool_result":
				out := raw["output"]
				seq++
				outCh <- events.NewToolResultEvent(seq, now, fmt.Sprintf("call_%d", seq-1), out, false)

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

			case "token_usage", "usage":
				var inTok, outTok int
				if i, ok := raw["input_tokens"].(float64); ok {
					inTok = int(i)
				}
				if o, ok := raw["output_tokens"].(float64); ok {
					outTok = int(o)
				}
				seq++
				outCh <- events.NewUsageEvent(seq, now, inTok, outTok, 0.0, "")

			case "session_end", "done":
				msg, _ := raw["message"].(string)
				if msg == "" {
					msg, _ = raw["result"].(string)
				}
				outcome, _ := raw["outcome"].(string)
				if outcome == "" {
					outcome = "completed"
				}
				seq++
				outCh <- events.NewDoneEvent(seq, now, msg, outcome)

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
