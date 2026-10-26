package system_test

import (
	"testing"

	"agent-dashboard/internal/core/system"
)

func TestCalculateDiskWarning(t *testing.T) {
	if system.CalculateDiskWarning(79.9) {
		t.Fatal("expected no warning at 79.9%")
	}
	if !system.CalculateDiskWarning(80.0) {
		t.Fatal("expected warning at 80.0%")
	}
	if !system.CalculateDiskWarning(95.5) {
		t.Fatal("expected warning at 95.5%")
	}
}
