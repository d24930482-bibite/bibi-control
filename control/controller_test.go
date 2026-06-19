package control

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSupervisor_WatchFiresOnDone verifies that Watch calls onExit exactly once
// when the done channel closes.
func TestSupervisor_WatchFiresOnDone(t *testing.T) {
	s := NewSupervisor()
	defer s.Stop()

	done := make(chan struct{})
	var count atomic.Int32

	s.Watch("node-1", done, func(id string) {
		if id != "node-1" {
			t.Errorf("onExit: got id=%q, want node-1", id)
		}
		count.Add(1)
	})

	close(done)

	// Give the goroutine time to run.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := count.Load(); got != 1 {
		t.Errorf("onExit called %d times, want 1", got)
	}
}

// TestSupervisor_CancelPreventsOnExit verifies that Cancel before done closes
// prevents onExit from being called.
func TestSupervisor_CancelPreventsOnExit(t *testing.T) {
	s := NewSupervisor()
	defer s.Stop()

	done := make(chan struct{})
	var count atomic.Int32

	s.Watch("node-cancel", done, func(_ string) {
		count.Add(1)
	})

	s.Cancel("node-cancel")

	// Now close done — onExit must NOT fire.
	close(done)
	time.Sleep(50 * time.Millisecond)

	if got := count.Load(); got != 0 {
		t.Errorf("onExit called %d times after Cancel, want 0", got)
	}
}

// TestSupervisor_StopJoinsWatchers verifies that Stop blocks until all
// goroutines have exited and is safe with live and already-fired watchers.
func TestSupervisor_StopJoinsWatchers(t *testing.T) {
	s := NewSupervisor()

	// One watcher whose process has already exited (done already closed).
	doneFired := make(chan struct{})
	close(doneFired)

	// One watcher whose process is still alive (done never closes).
	doneLive := make(chan struct{})

	var count atomic.Int32
	s.Watch("fired", doneFired, func(_ string) { count.Add(1) })
	s.Watch("live", doneLive, func(_ string) { count.Add(1) })

	// Stop must return quickly and join all goroutines.
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5 seconds")
	}

	// The live watcher was cancelled (not fired), fired watcher ran.
	// count may be 1 (fired) or 0 depending on whether Stop raced doneFired.
	// Either is valid — what matters is Stop returned without deadlock.
}

// TestSupervisor_Concurrent exercises Watch/Cancel/Stop concurrently under -race.
func TestSupervisor_Concurrent(t *testing.T) {
	const n = 20
	s := NewSupervisor()

	var wg sync.WaitGroup
	var count atomic.Int32

	for i := range n {
		nodeID := "node-" + string(rune('a'+i%26))
		done := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Watch(nodeID, done, func(_ string) { count.Add(1) })
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Half the time cancel, half the time close done (simulating process exit).
			if i%2 == 0 {
				s.Cancel(nodeID)
			} else {
				close(done)
			}
		}()
	}

	wg.Wait()
	s.Stop()
	// Just verifying we didn't race or deadlock; count is non-deterministic.
	_ = count.Load()
}

// TestSupervisor_WatchAfterStopIsNoop verifies Watch is a no-op after Stop.
func TestSupervisor_WatchAfterStopIsNoop(t *testing.T) {
	s := NewSupervisor()
	s.Stop()

	done := make(chan struct{})
	var count atomic.Int32
	s.Watch("post-stop", done, func(_ string) { count.Add(1) })
	close(done)

	time.Sleep(20 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Errorf("onExit called %d times after Stop+Watch, want 0", got)
	}
}

// TestSupervisor_ExactlyOnce_RaceDoneAndCancel verifies that even when done
// and cancel race, onExit fires at most once.
func TestSupervisor_ExactlyOnce_RaceDoneAndCancel(t *testing.T) {
	for range 100 {
		s := NewSupervisor()
		done := make(chan struct{})
		var count atomic.Int32

		s.Watch("race-node", done, func(_ string) { count.Add(1) })

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); close(done) }()
		go func() { defer wg.Done(); s.Cancel("race-node") }()
		wg.Wait()

		// Allow the goroutine to settle.
		time.Sleep(5 * time.Millisecond)
		s.Stop()

		if got := count.Load(); got > 1 {
			t.Fatalf("onExit called %d times in race, want 0 or 1", got)
		}
	}
}
