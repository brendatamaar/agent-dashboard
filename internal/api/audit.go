package api

import (
	"net/http"
	"strconv"

	"agent-dashboard/internal/store"
)

type AuditHandler struct {
	store *store.Store
}

func NewAuditHandler(s *store.Store) *AuditHandler {
	return &AuditHandler{store: s}
}

func (a *AuditHandler) HandleGetLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	logs, err := a.store.GetAuditLogs(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if logs == nil {
		logs = []store.AuditEntry{}
	}

	writeJSON(w, http.StatusOK, logs)
}
