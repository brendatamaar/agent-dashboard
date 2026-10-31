//go:build linux

package api

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
)

// Directory descriptors and O_NOFOLLOW keep checks and IO on the same inode,
// including when an agent concurrently replaces .claude or agents with symlinks.
func subagentDir(path string, create bool) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{".claude", "agents"} {
		if create {
			if e := unix.Mkdirat(fd, name, 0755); e != nil && !errors.Is(e, unix.EEXIST) {
				unix.Close(fd)
				return nil, e
			}
		}
		next, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "agents"), nil
}
func readSubagentDefs(path string) ([]SubagentDef, error) {
	dir, err := subagentDir(path, false)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var result []SubagentDef
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fd, e := unix.Openat(int(dir.Fd()), entry.Name(), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if e != nil {
			continue
		}
		file := os.NewFile(uintptr(fd), entry.Name())
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			file.Close()
			continue
		}
		data, e := io.ReadAll(io.LimitReader(file, 1<<20+1))
		file.Close()
		if e == nil && len(data) <= 1<<20 {
			result = append(result, SubagentDef{Name: strings.TrimSuffix(entry.Name(), ".md"), Content: string(data)})
		}
	}
	return result, nil
}
func writeSubagentDef(path, name string, data []byte) error {
	dir, err := subagentDir(path, true)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	tmp := generateID(".agentd-")
	fd, err := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0644)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(dir.Fd()), tmp, 0)
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return unix.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), name)
}
func deleteSubagentDef(path, name string) error {
	dir, err := subagentDir(path, false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	err = unix.Unlinkat(int(dir.Fd()), name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}
