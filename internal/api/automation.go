package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/shell/notifier"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type AutomationHandler struct {
	store *store.Store
	orch  *orchestrator.Orchestrator
}

func NewAutomationHandler(s *store.Store, orch *orchestrator.Orchestrator) *AutomationHandler {
	return &AutomationHandler{
		store: s,
		orch:  orch,
	}
}

// --- Templates ---

func (h *AutomationHandler) HandleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.store.ListTemplates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if templates == nil {
		templates = []store.Template{}
	}
	writeJSON(w, http.StatusOK, templates)
}

func (h *AutomationHandler) HandleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req store.Template
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "template name and prompt are required")
		return
	}
	if req.Harness == "" {
		req.Harness = "claude"
	}
	if req.ID == "" {
		req.ID = fmt.Sprintf("tmpl-%d", time.Now().UnixNano())
	}

	if err := h.store.CreateTemplate(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, req)
}

func (h *AutomationHandler) HandleGetTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tmpl, err := h.store.GetTemplate(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	writeJSON(w, http.StatusOK, tmpl)
}

func (h *AutomationHandler) HandleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req store.Template
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.ID = id
	if err := h.store.UpdateTemplate(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (h *AutomationHandler) HandleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteTemplate(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Schedules ---

func (h *AutomationHandler) HandleListSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := h.store.ListSchedules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if schedules == nil {
		schedules = []store.Schedule{}
	}
	writeJSON(w, http.StatusOK, schedules)
}

func (h *AutomationHandler) HandleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req store.Schedule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TemplateID == "" || req.RepoID == "" || req.Cron == "" {
		writeError(w, http.StatusBadRequest, "template_id, repo_id, and cron expression are required")
		return
	}

	nextRun, err := scheduler.NextCronTime(req.Cron, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid cron expression: %v", err))
		return
	}
	req.NextRunAt = &nextRun

	if req.ID == "" {
		req.ID = fmt.Sprintf("sch-%d", time.Now().UnixNano())
	}

	if err := h.store.CreateSchedule(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = h.orch.ReloadSchedules(r.Context())
	writeJSON(w, http.StatusCreated, req)
}

func (h *AutomationHandler) HandleGetSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sc, err := h.store.GetSchedule(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (h *AutomationHandler) HandleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req store.Schedule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.ID = id
	nextRun, err := scheduler.NextCronTime(req.Cron, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cron expression")
		return
	}
	req.NextRunAt = &nextRun

	if err := h.store.UpdateSchedule(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = h.orch.ReloadSchedules(r.Context())
	writeJSON(w, http.StatusOK, req)
}

func (h *AutomationHandler) HandleTriggerSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.orch.TriggerSchedule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "triggered"})
}

func (h *AutomationHandler) HandleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteSchedule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = h.orch.ReloadSchedules(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Harnesses ---

func (h *AutomationHandler) HandleListHarnesses(w http.ResponseWriter, r *http.Request) {
	reg := h.orch.HarnessRegistry()
	if reg == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	info := reg.CheckAll(r.Context())
	writeJSON(w, http.StatusOK, info)
}

func (h *AutomationHandler) HandleUpdateHarness(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	reg := h.orch.HarnessRegistry()
	if reg == nil {
		writeError(w, http.StatusInternalServerError, "harness registry not initialized")
		return
	}

	executor := func(ctx context.Context, cmd []string) (string, error) {
		c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
		out, err := c.CombinedOutput()
		return string(out), err
	}

	out, err := reg.UpdateHarness(r.Context(), name, executor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error",
			"error":  err.Error(),
			"output": out,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"output": out,
	})
}

// --- Notification Settings ---

func (h *AutomationHandler) HandleGetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	notif := h.orch.Notifier()
	if notif == nil {
		writeJSON(w, http.StatusOK, notifier.ChannelSettings{})
		return
	}
	writeJSON(w, http.StatusOK, notif.Settings())
}

func (h *AutomationHandler) HandleUpdateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	notif := h.orch.Notifier()
	if notif == nil {
		writeError(w, http.StatusInternalServerError, "notifier not initialized")
		return
	}

	var req notifier.ChannelSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := notif.SaveSettings(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, req)
}
