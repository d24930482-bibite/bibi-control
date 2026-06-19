// Package control provides the dead-process supervisor for workspace node
// lifecycles. The Supervisor watches one goroutine per active node; when the
// node's OS process exits it fires an onExit callback exactly once (guaranteed
// by sync.Once) and then exits the goroutine. It is intentionally
// dependency-light: it knows only a node id and an exit channel, and the
// callback closure (defined in workspace) carries all workspace-specific logic.
// This keeps the import graph acyclic: workspace imports control; control must
// NOT import workspace.
package control

import (
	"sync"
)

// Supervisor watches registered node processes and fires a callback when each
// exits. It is owned by the Workspace and started in Create/Open; stopped in
// Close before the manual node drain so no concurrent-map-write panic can
// occur.
type Supervisor struct {
	mu      sync.Mutex
	entries map[string]*watchEntry
	wg      sync.WaitGroup
	stopped bool
}

type watchEntry struct {
	once   sync.Once
	cancel chan struct{}
}

// NewSupervisor allocates a Supervisor ready to accept Watch calls.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		entries: make(map[string]*watchEntry),
	}
}

// Watch registers a watcher for nodeID. When the done channel closes (the OS
// process exited), onExit(nodeID) is called exactly once and the goroutine
// exits. If Cancel(nodeID) is called before done closes, onExit is never
// called. If the Supervisor is already stopped, Watch is a no-op.
//
// Each nodeID may be registered at most once at a time; registering the same
// nodeID a second time without first cancelling the old watcher is a caller
// error (the old watcher remains active).
func (s *Supervisor) Watch(nodeID string, done <-chan struct{}, onExit func(nodeID string)) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	e := &watchEntry{cancel: make(chan struct{})}
	s.entries[nodeID] = e
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		select {
		case <-done:
			// Process exited — fire the callback exactly once.
			e.once.Do(func() { onExit(nodeID) })
		case <-e.cancel:
			// Caller cancelled (clean stop/kill) — do not call onExit.
		}
		// Remove the entry from the map so Cancel is idempotent after fire.
		s.mu.Lock()
		if s.entries[nodeID] == e {
			delete(s.entries, nodeID)
		}
		s.mu.Unlock()
	}()
}

// Cancel deregisters the watcher for nodeID without calling onExit. It is a
// no-op if nodeID has no active watcher or if it has already fired.
func (s *Supervisor) Cancel(nodeID string) {
	s.mu.Lock()
	e, ok := s.entries[nodeID]
	if ok {
		delete(s.entries, nodeID)
	}
	s.mu.Unlock()
	if ok {
		// Closing the cancel channel signals the goroutine to exit without
		// calling onExit. sync.Once on the goroutine side guarantees onExit
		// fires at most once even if done and cancel race.
		e.once.Do(func() {}) // consume the Once so onExit can never fire
		close(e.cancel)
	}
}

// Stop cancels all active watchers and blocks until every watcher goroutine
// has exited. It is safe to call with a mix of live and already-fired
// watchers. After Stop returns, Watch is a no-op.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	entries := s.entries
	s.entries = make(map[string]*watchEntry)
	s.mu.Unlock()

	// Cancel all remaining watchers without calling onExit.
	for _, e := range entries {
		e.once.Do(func() {}) // consume the Once
		close(e.cancel)
	}
	s.wg.Wait()
}
