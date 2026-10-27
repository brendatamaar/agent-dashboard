package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NextCronTime computes the earliest time strictly after `from` that matches cronExpr.
// Supports standard 5-field expressions (min hour dom month dow) and aliases (@hourly, @daily, etc.).
func NextCronTime(cronExpr string, from time.Time) (time.Time, error) {
	expr := strings.TrimSpace(cronExpr)
	switch expr {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	case "@yearly", "@annually":
		expr = "0 0 1 1 *"
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("invalid cron expression: expected 5 fields, got %d", len(fields))
	}

	matchMin, err := parseField(fields[0], 0, 59)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid minute field: %w", err)
	}
	matchHour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid hour field: %w", err)
	}
	matchDom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day of month field: %w", err)
	}
	matchMonth, err := parseField(fields[3], 1, 12)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid month field: %w", err)
	}
	matchDow, err := parseField(fields[4], 0, 6)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day of week field: %w", err)
	}

	// Advance starting candidate to next full minute (truncated to seconds = 0)
	candidate := from.Truncate(time.Minute).Add(time.Minute)

	// Search up to 5 years ahead
	limit := candidate.AddDate(5, 0, 0)

	for candidate.Before(limit) {
		if !matchMonth[int(candidate.Month())] {
			// Advance to next month
			candidate = time.Date(candidate.Year(), candidate.Month()+1, 1, 0, 0, 0, 0, candidate.Location())
			continue
		}
		if !matchDom[candidate.Day()] || !matchDow[int(candidate.Weekday())] {
			// Advance to next day
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()+1, 0, 0, 0, 0, candidate.Location())
			continue
		}
		if !matchHour[candidate.Hour()] {
			// Advance to next hour
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour()+1, 0, 0, 0, candidate.Location())
			continue
		}
		if !matchMin[candidate.Minute()] {
			candidate = candidate.Add(time.Minute)
			continue
		}
		return candidate, nil
	}

	return time.Time{}, fmt.Errorf("no matching time found within search limit")
}

func parseField(field string, min, max int) (map[int]bool, error) {
	result := make(map[int]bool)
	if field == "*" {
		for i := min; i <= max; i++ {
			result[i] = true
		}
		return result, nil
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		if strings.HasPrefix(part, "*/") {
			step, err := strconv.Atoi(part[2:])
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step: %s", part)
			}
			for i := min; i <= max; i += step {
				result[i] = true
			}
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range: %s", part)
			}
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 != nil || err2 != nil || start > end || start < min || end > max {
				return nil, fmt.Errorf("invalid range bounds: %s", part)
			}
			for i := start; i <= end; i++ {
				result[i] = true
			}
			continue
		}

		val, err := strconv.Atoi(part)
		if err != nil || val < min || val > max {
			return nil, fmt.Errorf("value %s out of range [%d, %d]", part, min, max)
		}
		result[val] = true
	}

	return result, nil
}
