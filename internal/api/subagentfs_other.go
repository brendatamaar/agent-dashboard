//go:build !linux

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Development fallback rejects symlink ancestors. Production Linux uses dirfds.
func subagentPath(root string, create bool) (string, error) {
	path := root
	for _, name := range []string{".claude", "agents"} {
		path = filepath.Join(path, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && create {
			if err = os.Mkdir(path, 0755); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("unsafe directory")
		}
	}
	return path, nil
}
func readSubagentDefs(root string) ([]SubagentDef, error) {
	path, err := subagentPath(root, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var defs []SubagentDef
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 || e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(path, e.Name()))
		if err == nil {
			defs = append(defs, SubagentDef{Name: strings.TrimSuffix(e.Name(), ".md"), Content: string(data)})
		}
	}
	return defs, nil
}
func writeSubagentDef(root, name string, data []byte) error {
	path, err := subagentPath(root, true)
	if err != nil {
		return err
	}
	target := filepath.Join(path, name)
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe file")
	}
	f, err := os.CreateTemp(path, ".agentd-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, target)
}
func deleteSubagentDef(root, name string) error {
	path, err := subagentPath(root, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(path, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
