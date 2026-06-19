package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/simctl"
)

// testCtx returns a context with a generous timeout and registers cleanup.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newTestServer starts a stubSim over a real loopback listener and returns a
// simctl.Client connected to it. The server is stopped via t.Cleanup.
func newTestServer(t *testing.T) *simctl.Client {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := newStubSim(60)

	// Start the server in the background.
	done := make(chan struct{})
	go func() {
		defer close(done)
		serve(ctx, ln, s)
	}()

	t.Cleanup(func() {
		cancel()
		ln.Close()
		<-done
	})

	// Dial and wrap in a simctl.Client.
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	sess, err := ipc.Dial(dialCtx, ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	return simctl.New(sess)
}

func TestInfo(t *testing.T) {
	c := newTestServer(t)
	res, err := c.Info(testCtx(t))
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if res.TPS != 60 {
		t.Errorf("TPS = %v, want 60", res.TPS)
	}
	// SimTime should be non-negative (starts at 0 and may have advanced slightly).
	if res.SimTime < 0 {
		t.Errorf("SimTime = %v, want >= 0", res.SimTime)
	}
	if res.Paused {
		t.Errorf("Paused = true, want false on startup")
	}
}

func TestStop(t *testing.T) {
	c := newTestServer(t)
	ctx := testCtx(t)

	res, err := c.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if res.PreviousTimeScale <= 0 {
		t.Errorf("PreviousTimeScale = %v, want > 0", res.PreviousTimeScale)
	}

	// After STOP, INFO should report paused=true.
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("Info after Stop: %v", err)
	}
	if !info.Paused {
		t.Errorf("Paused = false after STOP, want true")
	}
}

func TestResume(t *testing.T) {
	c := newTestServer(t)
	ctx := testCtx(t)

	// Stop first so we have something to resume from.
	if _, err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	res, err := c.Resume(ctx, 5.0)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.TimeScale != 5.0 {
		t.Errorf("TimeScale = %v, want 5.0", res.TimeScale)
	}

	// After RESUME, INFO should report paused=false.
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("Info after Resume: %v", err)
	}
	if info.Paused {
		t.Errorf("Paused = true after RESUME, want false")
	}
}

func TestReload(t *testing.T) {
	c := newTestServer(t)
	res, err := c.Reload(testCtx(t))
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !res.Ok {
		t.Errorf("Ok = false, want true")
	}
	if res.Save == "" {
		t.Errorf("Save is empty, want a non-empty path")
	}
}

func TestUnknownCommand(t *testing.T) {
	// Start one server and dial it with a raw session to send an unknown command.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := newStubSim(60)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serve(ctx, ln, s)
	}()
	t.Cleanup(func() {
		cancel()
		ln.Close()
		<-done
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	sess, err := ipc.Dial(dialCtx, ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	var out struct{}
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()
	err = sess.Request(reqCtx, "NOPE", nil, &out)
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
	if !errors.Is(err, ipc.ErrRequestFailed) {
		t.Errorf("error = %v, want ipc.ErrRequestFailed", err)
	}
}

func TestResumeRejectsZeroTimeScale(t *testing.T) {
	c := newTestServer(t)
	_, err := c.Resume(testCtx(t), 0)
	if err == nil {
		t.Fatal("expected error for TimeScale=0, got nil")
	}
	if !errors.Is(err, ipc.ErrRequestFailed) {
		t.Errorf("error = %v, want ipc.ErrRequestFailed", err)
	}
}
