package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/coder/websocket"

	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type WSHandler struct {
	store *store.Store
	orch  *orchestrator.Orchestrator
}

func NewWSHandler(s *store.Store, orch *orchestrator.Orchestrator) *WSHandler {
	return &WSHandler{
		store: s,
		orch:  orch,
	}
}

// HandleEventsWS streams live events over WebSocket.
func (h *WSHandler) HandleEventsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Internal single-user Tailscale connection
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	ctx := conn.CloseRead(r.Context())
	agentID := r.URL.Query().Get("agent_id")

	evCh, unsubscribe := h.orch.Broadcaster().Subscribe(agentID)
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

type TerminalControlMsg struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// HandleTerminalWS bridges an xterm.js terminal session to a tmux PTY over WebSocket.
func (h *WSHandler) HandleTerminalWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "terminal session closed")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	agentID := r.URL.Query().Get("agent_id")
	var sessionName string

	if agentID != "" {
		a, ok := h.orch.GetAgent(agentID)
		if !ok || a.TmuxSession == "" {
			_ = conn.Write(ctx, websocket.MessageText, []byte("\r\nAgent tmux session not found.\r\n"))
			conn.Close(websocket.StatusNormalClosure, "session not found")
			return
		}
		sessionName = a.TmuxSession
	} else {
		// General host shell session
		sessionName = "agentd-host-shell"
		// Ensure host shell session exists in runtime
		_, _ = h.orch.Runtime().Create(ctx, runtime.RuntimeSpec{
			SessionName: sessionName,
			WorkDir:     "",
			Command:     nil, // opens default user shell
		})
	}

	pty, err := h.orch.Runtime().Attach(ctx, runtime.Handle(sessionName))
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("\r\nFailed to attach to terminal: %v\r\n", err)))
		conn.Close(websocket.StatusNormalClosure, "attach failed")
		return
	}
	defer pty.Close()

	// 1. PTY -> WebSocket reader
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := pty.Read(buf)
				if n > 0 {
					if writeErr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); writeErr != nil {
						cancel()
						return
					}
				}
				if err != nil {
					if err != io.EOF {
						_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("\r\nPTY closed: %v\r\n", err)))
					}
					cancel()
					return
				}
			}
		}
	}()

	// 2. WebSocket -> PTY writer
	for {
		msgType, msgBytes, err := conn.Read(ctx)
		if err != nil {
			break
		}

		if msgType == websocket.MessageText {
			// Check if control/resize message
			var ctrl TerminalControlMsg
			if jsonErr := json.Unmarshal(msgBytes, &ctrl); jsonErr == nil && ctrl.Type == "resize" {
				if ctrl.Rows > 0 && ctrl.Cols > 0 {
					_ = pty.Resize(ctrl.Rows, ctrl.Cols)
				}
				continue
			}
		}

		// Write user keyboard input to PTY
		if _, writeErr := pty.Write(msgBytes); writeErr != nil {
			break
		}
	}
}
