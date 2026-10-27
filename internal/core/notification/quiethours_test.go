package notification_test

import (
	"testing"
	"time"

	"agent-dashboard/internal/core/notification"
)

func TestQuietHoursOvernight(t *testing.T) {
	cfg := notification.QuietHoursConfig{
		Enabled:   true,
		StartHour: 22,
		StartMin:  0,
		EndHour:   7,
		EndMin:    0,
		Timezone:  "UTC",
	}

	// 23:30 is quiet
	t1 := time.Date(2026, 10, 27, 23, 30, 0, 0, time.UTC)
	if !notification.IsQuietHours(t1, cfg) {
		t.Errorf("expected 23:30 to be quiet")
	}

	// 06:15 is quiet
	t2 := time.Date(2026, 10, 28, 6, 15, 0, 0, time.UTC)
	if !notification.IsQuietHours(t2, cfg) {
		t.Errorf("expected 06:15 to be quiet")
	}

	// 12:00 is NOT quiet
	t3 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if notification.IsQuietHours(t3, cfg) {
		t.Errorf("expected 12:00 to NOT be quiet")
	}

	// Delivery rules: normal suppressed in quiet hours, urgent bypasses
	if notification.ShouldDeliver(t1, notification.PriorityNormal, cfg) {
		t.Errorf("expected normal priority to be suppressed during quiet hours")
	}
	if !notification.ShouldDeliver(t1, notification.PriorityUrgent, cfg) {
		t.Errorf("expected urgent priority to bypass quiet hours")
	}
}

func TestQuietHoursSameDay(t *testing.T) {
	cfg := notification.QuietHoursConfig{
		Enabled:   true,
		StartHour: 13,
		StartMin:  0,
		EndHour:   15,
		EndMin:    0,
		Timezone:  "UTC",
	}

	// 14:00 is quiet
	t1 := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)
	if !notification.IsQuietHours(t1, cfg) {
		t.Errorf("expected 14:00 to be quiet")
	}

	// 16:00 is NOT quiet
	t2 := time.Date(2026, 10, 27, 16, 0, 0, 0, time.UTC)
	if notification.IsQuietHours(t2, cfg) {
		t.Errorf("expected 16:00 to NOT be quiet")
	}

	// Disabled config should never be quiet
	cfg.Enabled = false
	if notification.IsQuietHours(t1, cfg) {
		t.Errorf("expected disabled quiet hours to return false")
	}
}
