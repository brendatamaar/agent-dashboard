package notification

import (
	"time"
)

type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityUrgent Priority = "urgent"
)

type QuietHoursConfig struct {
	Enabled   bool   `json:"enabled"`
	StartHour int    `json:"start_hour"` // 0-23
	StartMin  int    `json:"start_min"`  // 0-59
	EndHour   int    `json:"end_hour"`   // 0-23
	EndMin    int    `json:"end_min"`    // 0-59
	Timezone  string `json:"timezone"`   // IANA time zone (e.g. "UTC", "America/New_York")
}

// IsQuietHours returns true if the specified time falls within the configured quiet window.
func IsQuietHours(t time.Time, cfg QuietHoursConfig) bool {
	if !cfg.Enabled {
		return false
	}

	// The shell supplies t already converted to the configured timezone.
	localTime := t
	currentMinutes := localTime.Hour()*60 + localTime.Minute()
	startMinutes := cfg.StartHour*60 + cfg.StartMin
	endMinutes := cfg.EndHour*60 + cfg.EndMin

	if startMinutes < endMinutes {
		// Same day window (e.g. 13:00 to 15:00)
		return currentMinutes >= startMinutes && currentMinutes < endMinutes
	} else if startMinutes > endMinutes {
		// Overnight window (e.g. 22:00 to 07:00)
		return currentMinutes >= startMinutes || currentMinutes < endMinutes
	}

	// If start == end and enabled, 24h quiet
	return true
}

// ShouldDeliver evaluates whether a notification with given priority should be delivered immediately.
func ShouldDeliver(t time.Time, p Priority, cfg QuietHoursConfig) bool {
	if !IsQuietHours(t, cfg) {
		return true
	}
	// Urgent notifications (e.g. approvals, fatal errors) bypass quiet hours
	return p == PriorityUrgent
}
