package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/store"
)

func TestTemplatesAndSchedulesStore(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	// 1. Create repo for schedule foreign key
	repo := store.Repo{
		ID:        "repo-test-1",
		Name:      "test-repo",
		RemoteURL: "https://github.com/example/test-repo.git",
		Path:      "/tmp/test-repo",
	}
	if err := s.CreateRepo(ctx, &repo); err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}

	// 2. Create template
	tmpl := store.Template{
		ID:           "tmpl-1",
		Name:         "Nightly PR Review",
		Harness:      "claude",
		Prompt:       "review changes and open PR",
		SettingsJSON: `{"model":"claude-sonnet-5-5"}`,
	}
	if err := s.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	gotTmpl, err := s.GetTemplate(ctx, "tmpl-1")
	if err != nil {
		t.Fatalf("failed to get template: %v", err)
	}
	if gotTmpl.Name != "Nightly PR Review" || gotTmpl.Harness != "claude" {
		t.Errorf("unexpected template: %+v", gotTmpl)
	}

	tmplList, err := s.ListTemplates(ctx)
	if err != nil || len(tmplList) != 1 {
		t.Fatalf("expected 1 template in list, got %d (err: %v)", len(tmplList), err)
	}

	// 3. Create schedule
	now := time.Now().UTC()
	nextRun := now.Add(1 * time.Hour)
	sch := store.Schedule{
		ID:         "sch-1",
		TemplateID: "tmpl-1",
		RepoID:     "repo-test-1",
		Cron:       "0 2 * * *",
		Enabled:    true,
		NextRunAt:  &nextRun,
	}
	if err := s.CreateSchedule(ctx, sch); err != nil {
		t.Fatalf("failed to create schedule: %v", err)
	}

	gotSch, err := s.GetSchedule(ctx, "sch-1")
	if err != nil {
		t.Fatalf("failed to get schedule: %v", err)
	}
	if gotSch.Cron != "0 2 * * *" || !gotSch.Enabled {
		t.Errorf("unexpected schedule: %+v", gotSch)
	}

	schList, err := s.ListSchedules(ctx)
	if err != nil || len(schList) != 1 {
		t.Fatalf("expected 1 schedule in list, got %d (err: %v)", len(schList), err)
	}

	// Update schedule next run
	lastRun := now
	newNextRun := now.Add(24 * time.Hour)
	if err := s.UpdateScheduleNextRun(ctx, "sch-1", &lastRun, &newNextRun); err != nil {
		t.Fatalf("failed to update schedule next run: %v", err)
	}

	updatedSch, err := s.GetSchedule(ctx, "sch-1")
	if err != nil {
		t.Fatalf("failed to get updated schedule: %v", err)
	}
	if updatedSch.LastRunAt == nil || updatedSch.NextRunAt == nil {
		t.Fatalf("expected non-nil last_run_at and next_run_at")
	}

	// Delete
	if err := s.DeleteSchedule(ctx, "sch-1"); err != nil {
		t.Fatalf("failed to delete schedule: %v", err)
	}
	if err := s.DeleteTemplate(ctx, "tmpl-1"); err != nil {
		t.Fatalf("failed to delete template: %v", err)
	}
}
