package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"agent-dashboard/internal/shell/git"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type SecretDecryptor interface {
	DecryptSecret(ctx context.Context, name string) (string, error)
}

type GitHandler struct {
	store     *store.Store
	gitSvc    *git.Service
	orch      *orchestrator.Orchestrator
	decryptor SecretDecryptor
}

func NewGitHandler(st *store.Store, gitSvc *git.Service, orch *orchestrator.Orchestrator, decryptor SecretDecryptor) *GitHandler {
	return &GitHandler{
		store:     st,
		gitSvc:    gitSvc,
		orch:      orch,
		decryptor: decryptor,
	}
}

// HandleStatus returns the git status of an agent's worktree.
func (h *GitHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	st, err := h.gitSvc.Status(r.Context(), a.WorktreePath, a.BaseBranch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get git status: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, st)
}

// HandleChanges returns the list of modified/staged/untracked files.
func (h *GitHandler) HandleChanges(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	changes, err := h.gitSvc.ChangedFiles(r.Context(), a.WorktreePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list changes: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

// HandleDiff returns unified diff for a file or all changes.
func (h *GitHandler) HandleDiff(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	filePath := r.URL.Query().Get("file")
	staged := r.URL.Query().Get("staged") == "true"

	diffStr, err := h.gitSvc.UnifiedDiff(r.Context(), a.WorktreePath, filePath, staged)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get unified diff: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"diff": diffStr})
}

type StageRequest struct {
	Paths []string `json:"paths"`
}

// HandleStage stages files in the agent's worktree.
func (h *GitHandler) HandleStage(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var req StageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.gitSvc.Stage(r.Context(), a.WorktreePath, req.Paths); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to stage files: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleUnstage removes files from staging.
func (h *GitHandler) HandleUnstage(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var req StageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.gitSvc.Unstage(r.Context(), a.WorktreePath, req.Paths); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to unstage files: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type StageHunkRequest struct {
	Patch string `json:"patch"`
}

// HandleStageHunk applies a patch hunk directly to the index.
func (h *GitHandler) HandleStageHunk(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var req StageHunkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Patch == "" {
		writeError(w, http.StatusBadRequest, "patch content is required")
		return
	}

	if err := h.gitSvc.StageHunk(r.Context(), a.WorktreePath, req.Patch); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to stage hunk: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type CommitRequest struct {
	Message string `json:"message"`
	Amend   bool   `json:"amend"`
}

// HandleCommit commits staged changes.
func (h *GitHandler) HandleCommit(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var req CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.gitSvc.Commit(r.Context(), a.WorktreePath, req.Message, req.Amend); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("commit failed: %v", err))
		return
	}

	_ = h.store.WriteAudit(r.Context(), "user", "git_commit", a.ID, fmt.Sprintf("amend=%v msg=%s", req.Amend, req.Message))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type BranchRequest struct {
	Action string `json:"action"` // "create" | "switch"
	Branch string `json:"branch"`
}

// HandleBranch handles creating or switching branches.
func (h *GitHandler) HandleBranch(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var req BranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Branch == "" {
		writeError(w, http.StatusBadRequest, "branch and action are required")
		return
	}

	switch req.Action {
	case "create":
		if err := h.gitSvc.BranchCreate(r.Context(), a.WorktreePath, req.Branch); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("create branch failed: %v", err))
			return
		}
	case "switch":
		if err := h.gitSvc.BranchSwitch(r.Context(), a.WorktreePath, req.Branch); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("switch branch failed: %v", err))
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "action must be 'create' or 'switch'")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type PushRequest struct {
	Force       bool `json:"force"`
	ConfirmMain bool `json:"confirm_main"`
}

// HandlePush pushes changes to origin upstream using the dashboard's token.
func (h *GitHandler) HandlePush(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var req PushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	repoRec, err := h.store.GetRepo(r.Context(), a.RepoID)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	token, _ := h.decryptor.DecryptSecret(r.Context(), "github_token")

	head, err := h.gitSvc.Status(r.Context(), a.WorktreePath, a.BaseBranch)
	if err != nil || head.CurrentBranch == "" || head.CurrentBranch == "HEAD" {
		writeError(w, http.StatusBadRequest, "cannot push detached or unknown HEAD")
		return
	}
	if err := h.gitSvc.Push(r.Context(), a.WorktreePath, token, head.CurrentBranch, req.Force, req.ConfirmMain, repoRec.RemoteURL); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("git push failed: %v", err))
		return
	}

	_ = h.store.WriteAudit(r.Context(), "user", "git_push", a.ID, fmt.Sprintf("branch=%s force=%v", head.CurrentBranch, req.Force))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandlePullRebase pulls changes from base branch and rebases.
func (h *GitHandler) HandlePullRebase(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	repoRec, err := h.store.GetRepo(r.Context(), a.RepoID)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	token, _ := h.decryptor.DecryptSecret(r.Context(), "github_token")

	if err := h.gitSvc.PullRebase(r.Context(), a.WorktreePath, token, a.BaseBranch, repoRec.RemoteURL); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("git pull rebase failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type CreatePRRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// HandleCreatePR opens a GitHub pull request using the dashboard's token.
func (h *GitHandler) HandleCreatePR(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	a, ok := h.orch.GetAgent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	repoRec, err := h.store.GetRepo(r.Context(), a.RepoID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get repo details")
		return
	}

	var req CreatePRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		req.Title = fmt.Sprintf("Agent: %s", a.Branch)
	}
	if req.Body == "" {
		req.Body = fmt.Sprintf("Automated PR created by agent-dashboard\n\n**Agent Prompt:** %s", a.Prompt)
	}

	token, err := h.decryptor.DecryptSecret(r.Context(), "github_token")
	if err != nil || token == "" {
		writeError(w, http.StatusBadRequest, "github_token secret is required to open pull requests")
		return
	}

	head, err := h.gitSvc.Status(r.Context(), a.WorktreePath, a.BaseBranch)
	if err != nil || head.CurrentBranch == "" || head.CurrentBranch == "HEAD" {
		writeError(w, http.StatusBadRequest, "cannot create PR for detached or unknown HEAD")
		return
	}
	prResult, err := h.gitSvc.CreateGitHubPR(r.Context(), token, repoRec.RemoteURL, req.Title, req.Body, head.CurrentBranch, a.BaseBranch)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to open pull request: %v", err))
		return
	}

	_ = h.store.WriteAudit(r.Context(), "user", "github_pr", a.ID, fmt.Sprintf("pr_number=%d url=%s", prResult.Number, prResult.HTMLURL))
	writeJSON(w, http.StatusOK, map[string]any{"pr": prResult})
}
