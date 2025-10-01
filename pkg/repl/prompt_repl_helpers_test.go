//go:build !noprompt

package repl

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFlattenEditorText(t *testing.T) {
	cases := []struct {
		in   string
		out  string
		name string
	}{
		{"one line", "one line", "single"},
		{"line1\nline2", "line1 line2", "newline"},
		{"line1\r\nline2\rline3\twith\ttabs", "line1 line2 line3 with tabs", "crlf_cr_tab"},
		{"  spaced\n\t\ttext  ", "spaced text", "trim"},
	}
	for _, tc := range cases {
		if got := flattenEditorText(tc.in); got != tc.out {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.out, got)
		}
	}
}

func TestCtrlXCtrlETriggered(t *testing.T) {
	now := time.Now()
	th := 1500 * time.Millisecond
	if ctrlXCtrlETriggered(time.Time{}, now, th) {
		t.Fatalf("zero time should not trigger")
	}
	last := now.Add(-1400 * time.Millisecond)
	if !ctrlXCtrlETriggered(last, now, th) {
		t.Fatalf("should trigger within threshold")
	}
	last = now.Add(-1600 * time.Millisecond)
	if ctrlXCtrlETriggered(last, now, th) {
		t.Fatalf("should not trigger beyond threshold")
	}
}

func TestDrainFD(t *testing.T) {
	// Create a pipe and write some bytes to the read end
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	r, w := p[0], p[1]
	defer func() { _ = unix.Close(r) }()
	defer func() { _ = unix.Close(w) }()
	data := []byte("\x1b[2;2Rjunk")
	if _, err := unix.Write(w, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Make read end non-blocking so drainFD works predictably
	if err := unix.SetNonblock(r, true); err != nil {
		// Best effort; continue anyway
		_ = err
	}
	// Drain it
	drainFD(r)
	// After draining, re-enable non-blocking (drainFD resets it) and a read should return 0 or EAGAIN
	_ = unix.SetNonblock(r, true)
	buf := make([]byte, 16)
	n, err := unix.Read(r, buf)
	if n > 0 {
		t.Fatalf("expected no bytes after drain, got %d", n)
	}
	if err != nil && err != unix.EAGAIN && err != unix.EWOULDBLOCK {
		t.Fatalf("unexpected read err after drain: %v", err)
	}
}
