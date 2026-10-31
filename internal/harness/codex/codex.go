package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness"
)

// Adapter command flags checked against installed codex 0.140.0 --help on
// 2026-10-30: exec --json, exec resume, --model and --dangerously-bypass-approvals-and-sandbox.
type Adapter struct {
	binaryPath string
}

func NewAdapter() *Adapter {
	return &Adapter{
		binaryPath: "codex",
	}
}

func (a *Adapter) Name() string {
	return "codex"
}

func (a *Adapter) Check(ctx context.Context) (harness.VersionInfo, harness.AuthStatus, error) {
	var ver harness.VersionInfo
	var auth harness.AuthStatus

	// 1. Version check
	cmdVer := exec.CommandContext(ctx, a.binaryPath, "--version")
	outVer, err := cmdVer.Output()
	if err != nil {
		return ver, auth, fmt.Errorf("failed to run codex --version: %w", err)
	}
	ver.Raw = strings.TrimSpace(string(outVer))
	fields := strings.Fields(ver.Raw)
	if len(fields) >= 2 && fields[0] == "codex-cli" {
		ver.Version = fields[1]
	} else if len(fields) > 0 {
		ver.Version = fields[len(fields)-1]
	}

	// 2. Auth check: check OPENAI_API_KEY or ~/.codex/auth.json
	if os.Getenv("OPENAI_API_KEY") != "" {
		auth.LoggedIn = true
		auth.AuthMethod = "api_key"
	} else {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			authPath := filepath.Join(homeDir, ".codex", "auth.json")
			if info, err := os.Stat(authPath); err == nil && info.Size() > 0 {
				auth.LoggedIn = true
				auth.AuthMethod = "chatgpt"
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
		if spec.AutoApprove {
			cmd = append(cmd, "--dangerously-bypass-approvals-and-sandbox")
		}
		if spec.Prompt != "" {
			cmd = append(cmd, spec.Prompt)
		}
		return cmd
	}

	// Task mode: uses codex exec --json
	cmd := []string{a.binaryPath, "exec", "--json"}
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	if spec.AutoApprove {
		cmd = append(cmd, "--dangerously-bypass-approvals-and-sandbox")
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
			cmd = append(cmd, "--dangerously-bypass-approvals-and-sandbox")
		}
		return cmd
	}

	// Task mode: codex exec resume [SESSION_ID] [PROMPT] --json
	cmd := []string{a.binaryPath, "exec", "resume", sessionID}
	if spec.Prompt != "" {
		cmd = append(cmd, spec.Prompt)
	}
	cmd = append(cmd, "--json")
	if spec.Model != "" {
		cmd = append(cmd, "--model", spec.Model)
	}
	if spec.AutoApprove {
		cmd = append(cmd, "--dangerously-bypass-approvals-and-sandbox")
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
		defer harness.CloseOnCancel(ctx, r)()

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
				select {
				case errCh <- fmt.Errorf("unparseable harness output; use raw terminal: %w", err):
				default:
				}
				continue
			}

			now := time.Now().UTC()
			evType, _ := raw["type"].(string)
			evType = strings.ReplaceAll(evType, ".", "_")

			switch evType {
			case "thread_started", "session_init":
				seq++
				sessionID, _ := raw["thread_id"].(string)
				if sessionID == "" {
					sessionID, _ = raw["session_id"].(string)
				}
				ev := events.NewMessageEvent(seq, now, "system", "Session initialized")
				ev.Payload["session_id"] = sessionID
				if m, ok := raw["model"].(string); ok {
					ev.Payload["model"] = m
				}
				if !harness.SendEvent(ctx, outCh, ev) {
					return
				}

			case "item_started", "item_completed":
				item, _ := raw["item"].(map[string]any)
				kind, _ := item["type"].(string)
				id, _ := item["id"].(string)
				switch kind {
				case "agent_message":
					if evType == "item_completed" {
						text, _ := item["text"].(string)
						seq++
						if !harness.SendEvent(ctx, outCh, events.NewMessageEvent(seq, now, "assistant", text)) {
							return
						}
					}
				case "command_execution":
					seq++
					if evType == "item_started" {
						if !harness.SendEvent(ctx, outCh, events.NewToolCallEvent(seq, now, id, "shell", item["command"])) {
							return
						}
					} else {
						code, _ := item["exit_code"].(float64)
						if !harness.SendEvent(ctx, outCh, events.NewToolResultEvent(seq, now, id, item["aggregated_output"], code != 0)) {
							return
						}
					}
				default:
					select {
					case errCh <- fmt.Errorf("unsupported Codex item; use raw terminal"):
					default:
					}
				}
			case "turn_started":
				// Lifecycle event carries no normalized payload.
			case "turn_failed":
				seq++
				if !harness.SendEvent(ctx, outCh, events.NewErrorEvent(seq, now, "Codex turn failed", raw)) {
					return
				}
			case "item_added":
				if item, ok := raw["item"].(map[string]any); ok {
					itemType, _ := item["type"].(string)
					switch itemType {
					case "message":
						role, _ := item["role"].(string)
						var text string
						if content, ok := item["content"].([]any); ok {
							for _, c := range content {
								if cm, ok := c.(map[string]any); ok {
									if t, ok := cm["text"].(string); ok {
										text += t
									}
								}
							}
						} else if t, ok := item["text"].(string); ok {
							text = t
						}
						if text != "" {
							seq++
							if !harness.SendEvent(ctx, outCh, events.NewMessageEvent(seq, now, role, text)) {
								return
							}
						}

					case "function_call", "tool_call":
						callID, _ := item["call_id"].(string)
						name, _ := item["name"].(string)
						args := item["arguments"]
						seq++
						if !harness.SendEvent(ctx, outCh, events.NewToolCallEvent(seq, now, callID, name, args)) {
							return
						}

					case "function_call_output", "tool_result":
						callID, _ := item["call_id"].(string)
						out := item["output"]
						seq++
						if !harness.SendEvent(ctx, outCh, events.NewToolResultEvent(seq, now, callID, out, false)) {
							return
						}
					}
				}

			case "message":
				role, _ := raw["role"].(string)
				text, _ := raw["text"].(string)
				if text != "" {
					seq++
					if !harness.SendEvent(ctx, outCh, events.NewMessageEvent(seq, now, role, text)) {
						return
					}
				}

			case "subagent_started":
				subID, _ := raw["subagent_id"].(string)
				subType, _ := raw["subagent_type"].(string)
				prompt, _ := raw["prompt"].(string)
				parentID, _ := raw["parent_agent_id"].(string)
				seq++
				if !harness.SendEvent(ctx, outCh, events.NewSubagentStartedEvent(seq, now, subID, subType, prompt, parentID)) {
					return
				}

			case "subagent_finished":
				subID, _ := raw["subagent_id"].(string)
				result, _ := raw["result"].(string)
				success, _ := raw["success"].(bool)
				seq++
				if !harness.SendEvent(ctx, outCh, events.NewSubagentFinishedEvent(seq, now, subID, result, success)) {
					return
				}

			case "usage":
				var inTok, outTok int
				if u, ok := raw["usage"].(map[string]any); ok {
					if inVal, ok := u["input_tokens"].(float64); ok {
						inTok = int(inVal)
					}
					if outVal, ok := u["output_tokens"].(float64); ok {
						outTok = int(outVal)
					}
				}
				var cost float64
				if c, ok := raw["cost_usd"].(float64); ok {
					cost = c
				}
				seq++
				if !harness.SendEvent(ctx, outCh, events.NewUsageEvent(seq, now, inTok, outTok, cost, "")) {
					return
				}

			case "turn_completed", "done":
				if usage, ok := raw["usage"].(map[string]any); ok {
					input, _ := usage["input_tokens"].(float64)
					output, _ := usage["output_tokens"].(float64)
					seq++
					if !harness.SendEvent(ctx, outCh, events.NewUsageEvent(seq, now, int(input), int(output), 0, "")) {
						return
					}
				}
				res, _ := raw["result"].(string)
				seq++
				if !harness.SendEvent(ctx, outCh, events.NewDoneEvent(seq, now, res, "completed")) {
					return
				}

			case "error":
				msg, _ := raw["message"].(string)
				seq++
				if !harness.SendEvent(ctx, outCh, events.NewErrorEvent(seq, now, msg, raw)) {
					return
				}
			default:
				select {
				case errCh <- fmt.Errorf("unsupported harness event; use raw terminal"):
				default:
				}
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			atomic.AddInt64(&seq, 1)
			select {
			case errCh <- err:
			case <-ctx.Done():
			default:
			}
		}
	}()

	return outCh, errCh
}
