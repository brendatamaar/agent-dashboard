package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type ApprovalsHandler struct {
	store *store.Store
	orch  *orchestrator.Orchestrator
}

func NewApprovalsHandler(st *store.Store, orch *orchestrator.Orchestrator) *ApprovalsHandler {
	return &ApprovalsHandler{
		store: st,
		orch:  orch,
	}
}

// HandleList lists all approvals for a specific agent.
func (h *ApprovalsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	approvals, err := h.store.ListApprovals(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list approvals: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals})
}

type DecideApprovalRequest struct {
	Decision string `json:"decision"` // "approve" | "deny" | "always_allow"
}

// HandleDecide records user decision on an approval request.
func (h *ApprovalsHandler) HandleDecide(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	approvalID := r.PathValue("aid")
	if agentID == "" || approvalID == "" {
		writeError(w, http.StatusBadRequest, "agent id and approval id are required")
		return
	}

	var req DecideApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	approved := false
	always := false
	switch strings.ToLower(req.Decision) {
	case "approve":
		approved = true
	case "always_allow":
		approved = true
		always = true
	case "deny":
		approved = false
	default:
		writeError(w, http.StatusBadRequest, "decision must be 'approve', 'deny', or 'always_allow'")
		return
	}

	if err := h.orch.DecideApproval(r.Context(), agentID, approvalID, approved, always); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to record approval decision: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"decision": req.Decision,
	})
}

type HookApprovalRequest struct {
	AgentID   string         `json:"agent_id"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input,omitempty"`
	Command   string         `json:"command,omitempty"`
	FilePath  string         `json:"file_path,omitempty"`
	Diff      string         `json:"diff,omitempty"`
}

// HandleHookApproval is invoked by PreToolUse hooks to request approval from the user.
func (h *ApprovalsHandler) HandleHookApproval(w http.ResponseWriter, r *http.Request) {
	var req HookApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	apprID := fmt.Sprintf("app_%d", time.Now().UnixNano())
	apprReq := agent.ApprovalRequest{
		ID:        apprID,
		AgentID:   req.AgentID,
		ToolName:  req.ToolName,
		ToolInput: req.ToolInput,
		Command:   req.Command,
		FilePath:  req.FilePath,
		Diff:      req.Diff,
		CreatedAt: time.Now().UTC(),
	}

	action, err := h.orch.RequestApproval(r.Context(), apprReq)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"action": "deny", "error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"action": action})
}
