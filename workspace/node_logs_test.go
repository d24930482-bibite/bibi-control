package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/noderuntime"
)

// printfSpec returns a ProcessSpec for a short-lived process that writes
// deterministic lines to stdout. The test is skipped if /bin/sh is absent.
func printfSpec(t *testing.T, lines ...string) ipc.ProcessSpec {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available on this platform")
	}
	// Build a printf call: printf 'line one\nline two\n'
	arg := ""
	for _, l := range lines {
		arg += l + "\\n"
	}
	return ipc.ProcessSpec{
		Path: "/bin/sh",
		Args: []string{"-c", fmt.Sprintf("printf '%s'", arg)},
	}
}

// TestNodeLogs_CapturesStdout is the DoD test. It starts a real OS process
// that writes two lines to stdout and asserts NodeLogs returns both.
func TestNodeLogs_CapturesStdout(t *testing.T) {
	ctx := context.Background()
	ws, world := createTestWorkspaceAndWorld(t, ctx)
	defer ws.Close()

	proc := printfSpec(t, "line one", "line two")

	rt, _, err := ws.StartNode(ctx, StartNodeSpec{
		WorldID: world.ID,
		NodeID:  "log-node",
		Process: proc,
	})
	if err != nil {
		t.Fatalf("StartNode: %v", err)
	}

	// Wait for the process to exit so the exec output-copier goroutine has
	// fully flushed before we read the log buffer.
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := rt.Wait(waitCtx); err != nil {
		t.Logf("Wait returned: %v (process may have already exited)", err)
	}

	lines, err := ws.NodeLogs("log-node")
	if err != nil {
		t.Fatalf("NodeLogs: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("NodeLogs returned %d lines, want 2; lines: %v", len(lines), lines)
	}
	if lines[0].Text != "line one" {
		t.Errorf("lines[0].Text = %q, want %q", lines[0].Text, "line one")
	}
	if lines[1].Text != "line two" {
		t.Errorf("lines[1].Text = %q, want %q", lines[1].Text, "line two")
	}
	for i, l := range lines {
		if l.Level != "info" {
			t.Errorf("lines[%d].Level = %q, want %q", i, l.Level, "info")
		}
		if l.Time.IsZero() {
			t.Errorf("lines[%d].Time is zero", i)
		}
	}
}

// TestNodeLogs_Bounded pushes more than logRingDefaultMaxLines lines through a
// logBufferWriter and asserts the buffer is capped and the last written line is
// retained (oldest evicted).
func TestNodeLogs_Bounded(t *testing.T) {
	ring := newLogRing()
	w := &logBufferWriter{ring: ring, level: "info"}

	total := logRingDefaultMaxLines + 50
	for i := 0; i < total; i++ {
		line := fmt.Sprintf("line %d\n", i)
		n, err := w.Write([]byte(line))
		if n != len(line) || err != nil {
			t.Fatalf("Write(%q) = (%d, %v), want (%d, nil)", line, n, err, len(line))
		}
	}

	snap := ring.snapshot()
	if len(snap) != logRingDefaultMaxLines {
		t.Fatalf("snapshot length = %d, want %d", len(snap), logRingDefaultMaxLines)
	}

	// Last line written is "line <total-1>".
	want := fmt.Sprintf("line %d", total-1)
	if snap[len(snap)-1].Text != want {
		t.Errorf("last line = %q, want %q", snap[len(snap)-1].Text, want)
	}

	// First retained line should be "line 50" (oldest evicted are 0-49).
	wantFirst := fmt.Sprintf("line %d", total-logRingDefaultMaxLines)
	if snap[0].Text != wantFirst {
		t.Errorf("first retained line = %q, want %q", snap[0].Text, wantFirst)
	}
}

// TestNodeLogs_PartialLine verifies that a Write call without a trailing
// newline is buffered until the next Write supplies one.
func TestNodeLogs_PartialLine(t *testing.T) {
	ring := newLogRing()
	w := &logBufferWriter{ring: ring, level: "info"}

	// First write: no newline — should not flush any line yet.
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("Write(\"abc\") = (%d, %v), want (3, nil)", n, err)
	}
	if snap := ring.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot after partial write: got %d lines, want 0", len(snap))
	}

	// Second write: completes the line.
	if n, err := w.Write([]byte("def\n")); n != 4 || err != nil {
		t.Fatalf("Write(\"def\\n\") = (%d, %v), want (4, nil)", n, err)
	}
	snap := ring.snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot after completing write: got %d lines, want 1", len(snap))
	}
	if snap[0].Text != "abcdef" {
		t.Errorf("Text = %q, want %q", snap[0].Text, "abcdef")
	}
}

// TestNodeLogs_UnknownNode asserts that NodeLogs returns a non-nil error for
// a node id that has no buffer.
func TestNodeLogs_UnknownNode(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws, err := Create(ctx, root, "testowner", "testws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer ws.Close()

	_, err = ws.NodeLogs("ghost")
	if err == nil {
		t.Errorf("NodeLogs(\"ghost\") returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q does not mention node id", err.Error())
	}
}

// TestNodeLogs_DroppedOnStop starts a long-running node, stops it, and asserts
// that the log buffer is dropped (NodeLogs returns not-found).
func TestNodeLogs_DroppedOnStop(t *testing.T) {
	ctx := context.Background()
	ws, world := createTestWorkspaceAndWorld(t, ctx)
	defer ws.Close()

	proc := sleepSpec(t)

	_, _, err := ws.StartNode(ctx, StartNodeSpec{
		WorldID: world.ID,
		NodeID:  "stop-log-node",
		Process: proc,
	})
	if err != nil {
		t.Fatalf("StartNode: %v", err)
	}

	// Log buffer must exist while the node is active.
	if _, err := ws.NodeLogs("stop-log-node"); err != nil {
		t.Fatalf("NodeLogs before stop returned error: %v", err)
	}

	// Stop the node.
	if err := ws.StopNode(ctx, "stop-log-node", noderuntime.StopOptions{}); err != nil {
		t.Fatalf("StopNode: %v", err)
	}

	// Buffer must be gone.
	if _, err := ws.NodeLogs("stop-log-node"); err == nil {
		t.Errorf("NodeLogs after stop returned nil error, want not-found error")
	}
}
