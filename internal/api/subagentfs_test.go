package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubagentFilesystemCannotEscapeThroughSymlinks(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.md")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, ".claude")); err != nil {
		t.Skipf("symlink privileges unavailable: %v", err)
	}
	if err := writeSubagentDef(repo, "victim.md", []byte("overwritten")); err == nil {
		t.Fatal("followed symlink directory")
	}
	if _, err := readSubagentDefs(repo); err == nil {
		t.Fatal("listed through symlink directory")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "untouched" {
		t.Fatal("modified file outside repository")
	}
}
func TestSubagentRegularFileRoundTrip(t *testing.T) {
	repo := t.TempDir()
	if err := writeSubagentDef(repo, "reviewer.md", []byte("review carefully")); err != nil {
		t.Fatal(err)
	}
	defs, err := readSubagentDefs(repo)
	if err != nil || len(defs) != 1 || defs[0].Name != "reviewer" || defs[0].Content != "review carefully" {
		t.Fatalf("roundtrip: %v %v", defs, err)
	}
	if err := deleteSubagentDef(repo, "reviewer.md"); err != nil {
		t.Fatal(err)
	}
	defs, err = readSubagentDefs(repo)
	if err != nil || len(defs) != 0 {
		t.Fatalf("delete: %v %v", defs, err)
	}
}
