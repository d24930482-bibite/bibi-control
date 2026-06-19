package workspace

import (
	"bytes"
	"fmt"
	"sync"
	"time"
)

// LogLine is one captured output line from a node process.
type LogLine struct {
	Time  time.Time
	Level string // "info" for stdout, "error" for stderr
	Text  string
}

// logRingDefaultMaxLines is the capacity of the per-node line ring buffer.
const logRingDefaultMaxLines = 1000

// logRing is a bounded, line-oriented, thread-safe ring buffer for node
// output. Lines beyond maxLines evict the oldest entry (slice-front trim).
type logRing struct {
	mu       sync.Mutex
	maxLines int
	lines    []LogLine
	partial  []byte // bytes seen since the last '\n'
}

func newLogRing() *logRing {
	return &logRing{maxLines: logRingDefaultMaxLines}
}

// append adds a complete line (without the terminating newline) to the ring.
// Must be called with lr.mu held.
func (lr *logRing) appendLocked(line LogLine) {
	lr.lines = append(lr.lines, line)
	if len(lr.lines) > lr.maxLines {
		// Evict from the front to keep at most maxLines.
		lr.lines = lr.lines[len(lr.lines)-lr.maxLines:]
	}
}

// snapshot returns a copy of the current lines slice so the caller cannot race
// the writer.
func (lr *logRing) snapshot() []LogLine {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if len(lr.lines) == 0 {
		return nil
	}
	out := make([]LogLine, len(lr.lines))
	copy(out, lr.lines)
	return out
}

// logBufferWriter is an io.Writer adapter that splits its input on newlines and
// pushes each complete line into the associated logRing. It is used as
// cmd.Stdout / cmd.Stderr, so it must never return a short write or an error —
// the exec output-copier goroutine would break if it did.
type logBufferWriter struct {
	ring  *logRing
	level string
}

// Write implements io.Writer (and ipc.Writer). It is safe for concurrent use
// from multiple goroutines (os/exec drives one goroutine per stream, but the
// ring.mu guarantees mutual exclusion).
func (w *logBufferWriter) Write(p []byte) (int, error) {
	w.ring.mu.Lock()
	defer w.ring.mu.Unlock()

	// Append the incoming bytes to any leftover partial line.
	buf := append(w.ring.partial, p...)
	w.ring.partial = w.ring.partial[:0]

	for {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			// No newline yet — stash the remainder as a partial line.
			w.ring.partial = append(w.ring.partial[:0], buf...)
			break
		}
		text := string(buf[:idx])
		// Strip a trailing '\r' (CRLF) if present.
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
		}
		w.ring.appendLocked(LogLine{
			Time:  time.Now().UTC(),
			Level: w.level,
			Text:  text,
		})
		buf = buf[idx+1:]
	}

	// Always report the full length written and no error.
	return len(p), nil
}

// logRingFor returns (lazily creating) the logRing for nodeID under logMu.
func (w *Workspace) logRingFor(nodeID string) *logRing {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	if w.nodeLogs == nil {
		w.nodeLogs = make(map[string]*logRing)
	}
	lr, ok := w.nodeLogs[nodeID]
	if !ok {
		lr = newLogRing()
		w.nodeLogs[nodeID] = lr
	}
	return lr
}

// dropLogRing removes the log buffer for nodeID under logMu. Safe to call even
// if no buffer exists or the map is nil.
func (w *Workspace) dropLogRing(nodeID string) {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	if w.nodeLogs != nil {
		delete(w.nodeLogs, nodeID)
	}
}

// NodeLogs returns a snapshot of the captured output lines for the given node.
// The returned slice is a copy — callers may not mutate it.
//
// An error is returned when no log buffer exists for nodeID (either because the
// node was never started, was already stopped/killed, or never produced output
// and the buffer was pruned). This is distinct from "node with zero lines".
func (w *Workspace) NodeLogs(nodeID string) ([]LogLine, error) {
	if w == nil {
		return nil, fmt.Errorf("workspace: NodeLogs on nil workspace")
	}

	w.logMu.Lock()
	var lr *logRing
	if w.nodeLogs != nil {
		lr = w.nodeLogs[nodeID]
	}
	w.logMu.Unlock()

	if lr == nil {
		return nil, fmt.Errorf("workspace: NodeLogs: no log buffer for node %q", nodeID)
	}

	return lr.snapshot(), nil
}
