package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	agentsDir := filepath.Join(rp.Path, ".claude", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []SubagentDef{})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var defs []SubagentDef
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			name := strings.TrimSuffix(entry.Name(), ".md")
			filePath := filepath.Join(agentsDir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err == nil {
				defs = append(defs, SubagentDef{
					Name:    name,
					Content: string(data),
				})
			}
		}
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

	agentsDir := filepath.Join(rp.Path, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agents directory")
		return
	}

	filePath := filepath.Join(agentsDir, name+".md")
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write subagent definition")
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

	filePath := filepath.Join(rp.Path, ".claude", "agents", name+".md")
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, "failed to delete subagent definition")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
