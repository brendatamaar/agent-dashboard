package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"agent-dashboard/internal/core/repo"
	"agent-dashboard/internal/shell/git"
	"agent-dashboard/internal/store"
)

type ReposHandler struct {
	store     *store.Store
	gitSvc    *git.Service
	decryptor *SecretsHandler
}

func NewReposHandler(s *store.Store, gitSvc *git.Service, decryptor *SecretsHandler) *ReposHandler {
	return &ReposHandler{
		store:     s,
		gitSvc:    gitSvc,
		decryptor: decryptor,
	}
}

type AddRepoRequest struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

type RepoResponse struct {
	store.Repo
	CurrentBranch string `json:"current_branch"`
	IsDirty       bool   `json:"is_dirty"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
}

func (h *ReposHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	repos, err := h.store.ListRepos(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	res := make([]RepoResponse, 0, len(repos))
	for _, rp := range repos {
		item := RepoResponse{Repo: rp}
		status, err := h.gitSvc.Status(r.Context(), rp.Path, rp.DefaultBranch)
		if err == nil {
			item.CurrentBranch = status.CurrentBranch
			item.IsDirty = status.IsDirty
			item.Ahead = status.Ahead
			item.Behind = status.Behind
		} else {
			item.CurrentBranch = rp.DefaultBranch
		}
		res = append(res, item)
	}

	writeJSON(w, http.StatusOK, res)
}

func (h *ReposHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rp, err := h.store.GetRepo(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo not found"})
		return
	}

	item := RepoResponse{Repo: *rp}
	status, err := h.gitSvc.Status(r.Context(), rp.Path, rp.DefaultBranch)
	if err == nil {
		item.CurrentBranch = status.CurrentBranch
		item.IsDirty = status.IsDirty
		item.Ahead = status.Ahead
		item.Behind = status.Behind
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *ReposHandler) HandleAdd(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req AddRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	owner, repoName, err := repo.ParseAndValidateGitHubURL(req.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	targetName := req.Name
	if targetName == "" {
		targetName = repoName
	}
	if err := repo.ValidateRepoName(targetName); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Retrieve GitHub token if available
	token, _ := h.decryptor.DecryptSecret(r.Context(), "github_token")

	targetPath, err := h.gitSvc.Clone(r.Context(), req.URL, token, targetName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("failed to clone: %v", err)})
		return
	}

	// Inspect default branch
	status, _ := h.gitSvc.Status(r.Context(), targetPath, "main")
	defaultBranch := status.CurrentBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	id := fmt.Sprintf("repo-%d", time.Now().UnixNano())
	record := &store.Repo{
		ID:            id,
		Name:          targetName,
		RemoteURL:     req.URL,
		Path:          targetPath,
		DefaultBranch: defaultBranch,
		SettingsJSON:  "{}",
	}

	if err := h.store.CreateRepo(r.Context(), record); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist repo: " + err.Error()})
		return
	}

	_ = h.store.WriteAudit(r.Context(), user.Username, "repo_add", fmt.Sprintf("%s/%s", owner, repoName), "success")
	writeJSON(w, http.StatusCreated, record)
}

func (h *ReposHandler) HandleFetch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rp, err := h.store.GetRepo(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo not found"})
		return
	}

	token, _ := h.decryptor.DecryptSecret(r.Context(), "github_token")
	if err := h.gitSvc.Fetch(r.Context(), rp.Path, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "fetched"})
}

func (h *ReposHandler) HandlePull(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rp, err := h.store.GetRepo(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo not found"})
		return
	}

	token, _ := h.decryptor.DecryptSecret(r.Context(), "github_token")
	if err := h.gitSvc.Pull(r.Context(), rp.Path, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pulled"})
}

func (h *ReposHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	id := r.PathValue("id")
	rp, err := h.store.GetRepo(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo not found"})
		return
	}

	if err := h.store.DeleteRepo(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	_ = h.store.WriteAudit(r.Context(), user.Username, "repo_delete", rp.Name, "success")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
