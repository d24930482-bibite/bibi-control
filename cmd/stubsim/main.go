// Command stubsim is a standalone stub that acts as a real game node for demos
// and integration tests. It binds a TCP listener and speaks the exact IPC
// envelope contract (request→response/error) for INFO, STOP, RESUME, RELOAD,
// returning synthetic but plausible telemetry whose paused/tps/sim_time evolve
// in response to commands and advance over time. It periodically writes
// plausible log lines to stdout so U7's log-capture picks them up when launched
// as a node.
//
// # Address-discovery contract
//
// The stub binds the address given by --addr. Pass the same string as the
// compat_addr in workspace.start_node so the noderuntime dials the correct port:
//
//	workspace.start_node(world=…, path="/path/to/stubsim",
//	                     compat_addr="127.0.0.1:43100", connect=True)
//
// launched with: stubsim --addr 127.0.0.1:43100
//
// When --addr uses ephemeral port :0, the resolved host:port is printed to
// stdout as the first line so a human or launcher can read it. The fixed-addr
// path (matching compat_addr exactly) is the primary contract for U13.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/asemones/bibicontrol/ipc"
)

// stubSim holds the synthetic simulation state guarded by a single mutex.
// Serve goroutines and the ticker goroutine both read/write it; no lock is
// held across sess.Send (snapshot under lock, release, then send).
type stubSim struct {
	mu            sync.Mutex
	tps           float64
	paused        bool
	prevTimeScale float64
	simTime       float64
	lastRunAt     time.Time // last wall-clock instant while running (for advance)
	autosavePath  string
}

func newStubSim(tps float64) *stubSim {
	return &stubSim{
		tps:           tps,
		paused:        false,
		prevTimeScale: 1.0,
		simTime:       0,
		lastRunAt:     time.Now(),
		autosavePath:  "/saves/Autosaves/autosave_stub_00000.zip",
	}
}

// snapshot returns a point-in-time copy of state fields needed for INFO.
// It also advances simTime up to "now" while running.
func (s *stubSim) snapshot() (tps float64, realTPS float64, paused bool, simTime float64, autosave string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paused {
		now := time.Now()
		elapsed := now.Sub(s.lastRunAt).Seconds()
		s.simTime += elapsed * s.tps
		s.lastRunAt = now
	}
	jitter := 0.0
	if !s.paused {
		jitter = s.tps - rand.Float64()*2 // small jitter: tps ± 1
	}
	return s.tps, jitter, s.paused, s.simTime, s.autosavePath
}

func (s *stubSim) stop() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Advance simTime up to now before pausing.
	if !s.paused {
		now := time.Now()
		elapsed := now.Sub(s.lastRunAt).Seconds()
		s.simTime += elapsed * s.tps
		s.lastRunAt = now
	}
	s.paused = true
	return s.prevTimeScale
}

func (s *stubSim) resume(timeScale float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
	s.prevTimeScale = timeScale
	s.lastRunAt = time.Now()
}

// tick advances simTime and returns a snapshot for logging. Called by the
// background ticker goroutine.
func (s *stubSim) tick(elapsed time.Duration) (simTime float64, paused bool, tps float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paused {
		s.simTime += elapsed.Seconds() * s.tps
		s.lastRunAt = time.Now()
	}
	return s.simTime, s.paused, s.tps
}

// autosaveName returns the current autosave path for RELOAD.
func (s *stubSim) autosaveName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autosavePath
}

// mustJSON marshals v to JSON, panicking on error (identical to simctl_test.go:90).
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// handle processes one request envelope and returns (payload, errMsg).
// It must not hold s.mu across any network I/O — callers release the lock
// before calling sess.Send.
func (s *stubSim) handle(env ipc.Envelope) (json.RawMessage, string) {
	switch env.Command {
	case ipc.CommandInfo:
		tps, realTPS, paused, simTime, autosave := s.snapshot()
		return mustJSON(ipc.InfoResult{
			TPS:     tps,
			RealTPS: realTPS,
			Paused:  paused,
			SimTime: simTime,
			LastAutosave: &ipc.AutosaveInfo{
				Path:         autosave,
				Name:         filepath.Base(autosave),
				ModifiedUnix: time.Now().Unix(),
				Time:         time.Now().UTC().Format(time.RFC3339Nano),
			},
		}), ""

	case ipc.CommandStop:
		prev := s.stop()
		return mustJSON(ipc.StopResult{PreviousTimeScale: prev}), ""

	case ipc.CommandResume:
		var req ipc.ResumeRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return nil, err.Error()
		}
		if req.TimeScale <= 0 {
			return nil, "time_scale must be > 0"
		}
		s.resume(req.TimeScale)
		return mustJSON(ipc.ResumeResult{TimeScale: req.TimeScale}), ""

	case ipc.CommandReload:
		save := s.autosaveName()
		return mustJSON(ipc.ReloadResult{Save: save, Ok: true}), ""

	default:
		return nil, "unknown command: " + env.Command
	}
}

// serveConn wraps a single accepted connection as an ipc.Session and serves
// requests until the connection closes or ctx is cancelled.
func (s *stubSim) serveConn(ctx context.Context, conn net.Conn) {
	sess := ipc.NewSession(conn, nil)
	defer sess.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-sess.Events():
			if !ok {
				return
			}
			if env.Kind != ipc.KindRequest {
				continue
			}
			payload, errMsg := s.handle(env)
			reply := ipc.Envelope{Kind: ipc.KindResponse, ReplyTo: env.ID}
			if errMsg != "" {
				reply.Kind = ipc.KindError
				reply.Error = errMsg
			} else {
				reply.Payload = payload
			}
			_ = sess.Send(ctx, reply)
		}
	}
}

// serve runs the accept loop and background ticker until ctx is cancelled.
// Exported for testability; main() wraps it with signal-driven cancellation.
func serve(ctx context.Context, ln net.Listener, s *stubSim) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastTick := time.Now()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				elapsed := t.Sub(lastTick)
				lastTick = t
				simTime, paused, tps := s.tick(elapsed)
				fmt.Fprintf(os.Stdout, "[stubsim] t=%.1f tps=%.1f paused=%v autosave=%s\n",
					simTime, tps, paused, s.autosaveName())
			}
		}
	}()

	// Accept loop — closes when ln is closed (on ctx cancel in main).
	connCh := make(chan net.Conn)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				close(connCh)
				return
			}
			connCh <- conn
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case conn, ok := <-connCh:
			if !ok {
				return
			}
			go s.serveConn(ctx, conn)
		}
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "TCP bind address for IPC listener")
	tps := flag.Float64("tps", 60, "target TPS reported by INFO")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stubsim: listen %s: %v\n", *addr, err)
		os.Exit(1)
	}

	// If port was ephemeral (:0), print the resolved address as the first line
	// so a human or launcher can discover it.
	bound := ln.Addr().String()
	_, port, _ := net.SplitHostPort(bound)
	_, configPort, _ := net.SplitHostPort(*addr)
	if configPort == "0" || configPort == "" {
		fmt.Println(bound)
	}
	_ = port

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s := newStubSim(*tps)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	serve(ctx, ln, s)
}
