package store_test

import (
	"agent-dashboard/internal/store"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditScrubsKnownPatternsWithoutServerWiring(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	token := "ghp_" + strings.Repeat("A", 36)
	if err = st.WriteAudit(context.Background(), token, token, token, token); err != nil {
		t.Fatal(err)
	}
	records, err := st.GetAuditLogs(context.Background(), 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("audit rows: %v %v", records, err)
	}
	for _, value := range []string{records[0].Actor, records[0].Action, records[0].Target, records[0].Result} {
		if strings.Contains(value, token) {
			t.Fatal("audit store persisted known secret pattern")
		}
	}
	// The callback may update its own registration without reentering an held lock.
	st.SetAuditRedactor(func(v string) string {
		st.SetAuditRedactor(nil)
		return strings.ReplaceAll(v, "private-canary", "[redacted]")
	})
	if got := st.RedactAudit("private-canary"); strings.Contains(got, "private-canary") {
		t.Fatal("dynamic audit callback not applied")
	}
}
