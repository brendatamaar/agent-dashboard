package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type AgentsHandler struct {
	store *store.Store
	orch  *orchestrator.Orchestrator
}

func NewAgentsHandler(s *store.Store, orch *orchestrator.Orchestrator) *AgentsHandler {
	return &AgentsHandler{
		store: s,
		orch:  orch,
	}
}

type CreateAgentRequest struct {
	RepoID       string `json:"repo_id"`
	Harness      string `json:"harness"`
	Model        string `json:"model"`
	Mode         string `json:"mode"`
	Prompt       string `json:"prompt"`
	BaseBranch   string `json:"base_branch"`
	CustomBranch string `json:"branch"`
}

func (h *AgentsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	agents := h.orch.ListAgents()

	// Sort order: awaiting_approval (0), running (1), provisioning (2), idle (3), queued (4), others (5)
	statePriority := func(s agent.State) int {
		switch s {
		case agent.StateAwaitingApproval:
			return 0
		case agent.StateRunning:
			return 1
		case agent.StateProvisioning:
			return 2
		case agent.StateIdle:
			return 3
		case agent.StateQueued:
			return 4
		default:
			return 5
		}
	}

	sort.Slice(agents, func(i, j int) bool {
		pi := statePriority(agents[i].State)
		pj := statePriority(agents[j].State)
		if pi != pj {
			return pi < pj
		}
		return agents[i].UpdatedAt.After(agents[j].UpdatedAt)
	})

	writeJSON(w, http.StatusOK, agents)
}

func (h *AgentsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok := h.orch.GetAgent(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *AgentsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.RepoID == "" || req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo_id and prompt are required"})
		return
	}
	if req.Mode == "" {
		req.Mode = "task"
	}

	createdAgent, err := h.orch.SubmitAgent(
		r.Context(),
		req.RepoID,
		req.Harness,
		req.Model,
		req.Mode,
		req.Prompt,
		req.BaseBranch,
		req.CustomBranch,
	)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	_ = h.store.WriteAudit(r.Context(), user.Username, "agent_start", createdAgent.ID, "success")
	writeJSON(w, http.StatusCreated, createdAgent)
}

type SendMessageRequest struct {
	Message string `json:"message"`
}

func (h *AgentsHandler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if err := h.orch.SendMessage(r.Context(), id, req.Message); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (h *AgentsHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := r.PathValue("id")

	if err := h.orch.StopAgent(r.Context(), id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if user != nil {
		_ = h.store.WriteAudit(r.Context(), user.Username, "agent_stop", id, "success")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *AgentsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := r.PathValue("id")

	if err := h.orch.DeleteAgent(r.Context(), id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if user != nil {
		_ = h.store.WriteAudit(r.Context(), user.Username, "agent_delete", id, "success")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *AgentsHandler) HandleGetEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	afterSeqStr := r.URL.Query().Get("after_seq")
	var afterSeq int64
	if afterSeqStr != "" {
		afterSeq, _ = strconv.ParseInt(afterSeqStr, 10, 64)
	}

	records, err := h.store.GetEvents(r.Context(), id, afterSeq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if records == nil {
		records = []store.EventRecord{}
	}
	writeJSON(w, http.StatusOK, records)
}
