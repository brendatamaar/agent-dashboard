package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type WSHandler struct {
	store  *store.Store
	orch   *orchestrator.Orchestrator
	origin string
	mu     sync.Mutex
	conns  map[*websocket.Conn]context.CancelFunc
	closed bool
}

func NewWSHandler(s *store.Store, orch *orchestrator.Orchestrator, origins ...string) *WSHandler {
	origin := ""
	if len(origins) > 0 {
		origin = strings.TrimRight(origins[0], "/")
	}
	return &WSHandler{
		store:  s,
		orch:   orch,
		origin: origin, conns: make(map[*websocket.Conn]context.CancelFunc),
	}
}

func (h *WSHandler) accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, context.Context, func(), error) {
	origin := r.Header.Get("Origin")
	expected := h.origin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	if origin != "" && origin != expected {
		writeError(w, http.StatusForbidden, "websocket origin is not allowed")
		return nil, nil, nil, fmt.Errorf("origin denied")
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithCancel(r.Context())
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancel()
		_ = conn.CloseNow()
		return nil, nil, nil, fmt.Errorf("server shutting down")
	}
	h.conns[conn] = cancel
	h.mu.Unlock()
	cleanup := func() { cancel(); h.mu.Lock(); delete(h.conns, conn); h.mu.Unlock(); _ = conn.CloseNow() }
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cookie, err := r.Cookie("agentd_session")
				if err == nil {
					_, err = h.store.ValidateSession(ctx, cookie.Value)
				}
				if err != nil {
					cancel()
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	return conn, ctx, cleanup, nil
}
func (h *WSHandler) Close() {
	h.mu.Lock()
	h.closed = true
	connections := make(map[*websocket.Conn]context.CancelFunc, len(h.conns))
	for c, cancel := range h.conns {
		connections[c] = cancel
	}
	h.mu.Unlock()
	for c, cancel := range connections {
		cancel()
		_ = c.CloseNow()
	}
}

// HandleEventsWS streams live events over WebSocket.
func (h *WSHandler) HandleEventsWS(w http.ResponseWriter, r *http.Request) {
	conn, baseCtx, cleanup, err := h.accept(w, r)
	if err != nil {
		return
	}
	defer cleanup()
	ctx := conn.CloseRead(baseCtx)
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
	conn, baseCtx, cleanup, err := h.accept(w, r)
	if err != nil {
		return
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()
	agentID := r.URL.Query().Get("agent_id")
	var sessionName string
	var rt runtime.Runtime

	if agentID != "" {
		a, ok := h.orch.GetAgent(agentID)
		if !ok || a.TmuxSession == "" {
			_ = conn.Write(ctx, websocket.MessageText, []byte("\r\nAgent tmux session not found.\r\n"))
			_ = conn.Close(websocket.StatusNormalClosure, "session not found")
			return
		}
		sessionName = a.TmuxSession
		rt, err = h.orch.RuntimeForAgent(agentID)
		if err != nil {
			return
		}
	} else {
		// General host shell session uses the explicit host runtime.
		var ok bool
		rt, ok = h.orch.RuntimeByName("host")
		if !ok {
			return
		}
		sessionName = "agentd-host-shell"

	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		return
	}
	if err = h.store.WriteAudit(ctx, user.Username, "terminal_attach", sessionName, "requested"); err != nil {
		return
	}
	if agentID == "" {
		_, _ = rt.Create(ctx, runtime.RuntimeSpec{SessionName: sessionName, Mode: "interactive", Command: []string{"/bin/bash", "--noprofile", "--norc", "-i"}})
	}
	pty, err := rt.Attach(ctx, runtime.Handle(sessionName))
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("\r\nFailed to attach to terminal: %v\r\n", err)))
		_ = conn.Close(websocket.StatusNormalClosure, "attach failed")
		return
	}
	defer func() { _ = pty.Close() }()

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
