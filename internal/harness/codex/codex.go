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
				outCh <- ev

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
							outCh <- events.NewMessageEvent(seq, now, role, text)
						}

					case "function_call", "tool_call":
						callID, _ := item["call_id"].(string)
						name, _ := item["name"].(string)
						args := item["arguments"]
						seq++
						outCh <- events.NewToolCallEvent(seq, now, callID, name, args)

					case "function_call_output", "tool_result":
						callID, _ := item["call_id"].(string)
						out := item["output"]
						seq++
						outCh <- events.NewToolResultEvent(seq, now, callID, out, false)
					}
				}

			case "message":
				role, _ := raw["role"].(string)
				text, _ := raw["text"].(string)
				if text != "" {
					seq++
					outCh <- events.NewMessageEvent(seq, now, role, text)
				}

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
				outCh <- events.NewUsageEvent(seq, now, inTok, outTok, cost, "")

			case "turn_completed", "done":
				res, _ := raw["result"].(string)
				seq++
				outCh <- events.NewDoneEvent(seq, now, res, "completed")

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
