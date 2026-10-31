//go:build !windows

package host

import (
	"github.com/creack/pty"
	"testing"
)

func TestPTYResizeUsesActualTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	wrapper := &ptyWrapper{f: master}
	if err := wrapper.Resize(42, 91); err != nil {
		t.Fatal(err)
	}
	rows, cols, err := pty.Getsize(slave)
	if err != nil || rows != 42 || cols != 91 {
		t.Fatalf("PTY not resized: %dx%d %v", rows, cols, err)
	}
}
