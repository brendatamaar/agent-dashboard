package api

import (
	"encoding/json"
	"net/http"

	"agent-dashboard/internal/core/repo"
	"agent-dashboard/internal/store"
)

type SubagentsHandler struct {
	store *store.Store
}

func NewSubagentsHandler(s *store.Store) *SubagentsHandler {
	return &SubagentsHandler{store: s}
}

type SubagentDef struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (h *SubagentsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("id")
	rp, err := h.store.GetRepo(r.Context(), repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	defs, err := readSubagentDefs(rp.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsafe or unreadable subagent directory")
		return
	}

	if defs == nil {
		defs = []SubagentDef{}
	}
	writeJSON(w, http.StatusOK, defs)
}

func (h *SubagentsHandler) HandleSave(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("id")
	rp, err := h.store.GetRepo(r.Context(), repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	var req SubagentDef
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := repo.SanitizeSlug(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid subagent name")
		return
	}

	if err := writeSubagentDef(rp.Path, name+".md", []byte(req.Content)); err != nil {
		writeError(w, http.StatusBadRequest, "unsafe or unwritable subagent definition")
		return
	}

	writeJSON(w, http.StatusOK, SubagentDef{
		Name:    name,
		Content: req.Content,
	})
}

func (h *SubagentsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("id")
	subName := r.PathValue("name")

	rp, err := h.store.GetRepo(r.Context(), repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	name := repo.SanitizeSlug(subName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid subagent name")
		return
	}

	if err := deleteSubagentDef(rp.Path, name+".md"); err != nil {
		writeError(w, http.StatusBadRequest, "unsafe subagent definition")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
