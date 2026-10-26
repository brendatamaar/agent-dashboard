package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		binaryPath: "claude",
	}
}

func (a *Adapter) Name() string {
	return "claude"
}

func (a *Adapter) Check(ctx context.Context) (harness.VersionInfo, harness.AuthStatus, error) {
	var ver harness.VersionInfo
	var auth harness.AuthStatus

	// 1. Check version
	cmdVer := exec.CommandContext(ctx, a.binaryPath, "--version")
	outVer, err := cmdVer.Output()
	if err != nil {
		return ver, auth, fmt.Errorf("failed to run claude --version: %w", err)
	}
	ver.Raw = strings.TrimSpace(string(outVer))
	parts := strings.Fields(ver.Raw)
	if len(parts) > 0 {
		ver.Version = parts[0]
	}

	// 2. Check auth status via claude auth status --json
	cmdAuth := exec.CommandContext(ctx, a.binaryPath, "auth", "status", "--json")
	outAuth, err := cmdAuth.Output()
	if err == nil {
		var statusRaw struct {
			LoggedIn   bool   `json:"loggedIn"`
			AuthMethod string `json:"authMethod"`
			Email      string `json:"email"`
		}
		if jsonErr := json.Unmarshal(outAuth, &statusRaw); jsonErr == nil {
			auth.LoggedIn = statusRaw.LoggedIn
			auth.AuthMethod = statusRaw.AuthMethod
			auth.Email = statusRaw.Email
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
		return cmd
	}

	// Task mode: uses claude -p with streaming JSON
	// NOTE (ADR 0002): --output-format=stream-json requires --verbose
	cmd := []string{a.binaryPath, "-p", "--output-format=stream-json", "--verbose"}
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	if spec.Prompt != "" {
		cmd = append(cmd, spec.Prompt)
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

	// Task mode: resume with prompt
	cmd := []string{a.binaryPath, "-p", "--output-format=stream-json", "--verbose", "--resume", sessionID}
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
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

// ParseEvents reads newline-delimited JSON stream from Claude Code and converts into normalized events.
func (a *Adapter) ParseEvents(ctx context.Context, r io.Reader) (<-chan events.NormalizedEvent, <-chan error) {
	outCh := make(chan events.NormalizedEvent, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(outCh)
		defer close(errCh)

		scanner := bufio.NewScanner(r)
		// Set buffer to handle large JSON lines (e.g. diffs or file contents)
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

			msgType, _ := raw["type"].(string)
			now := time.Now().UTC()

			switch msgType {
			case "system":
				subType, _ := raw["subtype"].(string)
				if subType == "init" {
					currSeq := atomic.AddInt64(&seq, 1)
					ev := events.NormalizedEvent{
						Seq:       currSeq,
						Type:      events.EventMessage,
						Timestamp: now,
						Payload: map[string]any{
							"role":       "system",
							"session_id": raw["session_id"],
							"model":      raw["model"],
							"tools":      raw["tools"],
						},
					}
					outCh <- ev
				}

			case "assistant":
				msgObj, _ := raw["message"].(map[string]any)
				contentList, _ := msgObj["content"].([]any)
				for _, item := range contentList {
					contentMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					cType, _ := contentMap["type"].(string)
					switch cType {
					case "text":
						text, _ := contentMap["text"].(string)
						if strings.TrimSpace(text) != "" {
							currSeq := atomic.AddInt64(&seq, 1)
							outCh <- events.NewMessageEvent(currSeq, now, "assistant", text)
						}
					case "tool_use":
						toolID, _ := contentMap["id"].(string)
						toolName, _ := contentMap["name"].(string)
						input := contentMap["input"]
						currSeq := atomic.AddInt64(&seq, 1)
						outCh <- events.NewToolCallEvent(currSeq, now, toolID, toolName, input)
					}
				}

			case "user":
				msgObj, _ := raw["message"].(map[string]any)
				contentList, _ := msgObj["content"].([]any)
				for _, item := range contentList {
					contentMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					cType, _ := contentMap["type"].(string)
					if cType == "tool_result" {
						toolID, _ := contentMap["tool_use_id"].(string)
						content := contentMap["content"]
						isErr, _ := contentMap["is_error"].(bool)
						currSeq := atomic.AddInt64(&seq, 1)
						outCh <- events.NewToolResultEvent(currSeq, now, toolID, content, isErr)
					}
				}

			case "result":
				subType, _ := raw["subtype"].(string)
				resultText, _ := raw["result"].(string)
				termReason, _ := raw["terminal_reason"].(string)
				costUSD, _ := raw["total_cost_usd"].(float64)

				// Extract usage tokens if available
				usageMap, _ := raw["usage"].(map[string]any)
				var inTokens, outTokens int
				if usageMap != nil {
					if it, ok := usageMap["input_tokens"].(float64); ok {
						inTokens = int(it)
					}
					if ot, ok := usageMap["output_tokens"].(float64); ok {
						outTokens = int(ot)
					}
				}

				if subType == "success" || subType == "" {
					currSeq := atomic.AddInt64(&seq, 1)
					outCh <- events.NewUsageEvent(currSeq, now, inTokens, outTokens, costUSD, "")

					currSeq2 := atomic.AddInt64(&seq, 1)
					outCh <- events.NewDoneEvent(currSeq2, now, resultText, termReason)
				}

			case "error":
				errMsg, _ := raw["message"].(string)
				currSeq := atomic.AddInt64(&seq, 1)
				outCh <- events.NewErrorEvent(currSeq, now, errMsg, raw)
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			errCh <- err
		}
	}()

	return outCh, errCh
}

// Compile-time check that Adapter implements harness.Harness
var _ harness.Harness = (*Adapter)(nil)
