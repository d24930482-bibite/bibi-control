package noderuntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/asemones/bibicontrol/ipc"
)

var (
	ErrNoSession = errors.New("noderuntime: no compat session")
	ErrNoProcess = errors.New("noderuntime: no process")
)

// Spec describes one opaque game-node runtime instance.
//
// This package intentionally does not own node configuration, workspace layout,
// persistence, save semantics, UI state, or distributed-agent behavior. It only
// composes a local process handle with an optional TCP compat session.
type Spec struct {
	NodeID string
	RunID  string

	Process ipc.ProcessSpec

	// CompatAddr is the game-owned TCP endpoint, for example "127.0.0.1:43100".
	// If empty, Start only launches the process and leaves the runtime disconnected.
	CompatAddr string

	// ConnectOnStart controls whether Start dials CompatAddr before returning.
	// If CompatAddr is set and ConnectOnStart is false, callers may call Connect later.
	ConnectOnStart bool

	// DialTimeout bounds ConnectOnStart. Defaults to 10 seconds.
	DialTimeout time.Duration

	// DialInterval controls retry cadence while waiting for the game compat layer
	// to begin listening. Defaults to 200 milliseconds.
	DialInterval time.Duration

	Codec ipc.Codec
}

type Runtime struct {
	nodeID string
	runID  string

	compatAddr string
	codec      ipc.Codec

	mu      sync.RWMutex
	process *ipc.Process
	session *ipc.Session
}

type State struct {
	NodeID string `json:"node_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`

	PID     int             `json:"pid,omitempty"`
	Process ipc.ProcessInfo `json:"process"`

	CompatAddr string `json:"compat_addr,omitempty"`
	Connected  bool   `json:"connected"`
}

type StopOptions struct {
	// Command is an optional one-way compat command sent before waiting/killing.
	// Examples: "shutdown", "quit", "close". If empty, no graceful command is sent.
	Command string
	Payload any

	// GracePeriod is how long to wait after the graceful command before killing.
	// Defaults to 5 seconds when Command is set. Ignored when Command is empty.
	GracePeriod time.Duration

	// NoKillAfterGrace disables the default force-kill fallback after the grace
	// period expires. By default, Stop kills after grace.
	NoKillAfterGrace bool
}

func Start(ctx context.Context, spec Spec) (*Runtime, error) {
	proc, err := ipc.StartProcess(ctx, spec.Process)
	if err != nil {
		return nil, err
	}

	r := &Runtime{
		nodeID:     spec.NodeID,
		runID:      spec.RunID,
		compatAddr: spec.CompatAddr,
		codec:      spec.Codec,
		process:    proc,
	}

	if spec.CompatAddr != "" && spec.ConnectOnStart {
		connectCtx := ctx
		cancel := func() {}
		if spec.DialTimeout > 0 {
			connectCtx, cancel = context.WithTimeout(ctx, spec.DialTimeout)
		} else {
			connectCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		}
		defer cancel()

		if err := r.Connect(connectCtx, ConnectOptions{Interval: spec.DialInterval}); err != nil {
			_ = proc.Kill()
			return nil, err
		}
	}

	return r, nil
}

func Wrap(nodeID, runID string, proc *ipc.Process, sess *ipc.Session) *Runtime {
	return &Runtime{
		nodeID:  nodeID,
		runID:   runID,
		process: proc,
		session: sess,
	}
}

func (r *Runtime) NodeID() string { return r.nodeID }
func (r *Runtime) RunID() string  { return r.runID }

func (r *Runtime) Process() *ipc.Process {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.process
}

func (r *Runtime) Session() *ipc.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.session
}

func (r *Runtime) State() State {
	r.mu.RLock()
	proc := r.process
	sess := r.session
	compatAddr := r.compatAddr
	r.mu.RUnlock()

	var info ipc.ProcessInfo
	var pid int
	if proc != nil {
		info = proc.Info()
		pid = proc.PID()
	}

	return State{
		NodeID:     r.nodeID,
		RunID:      r.runID,
		PID:        pid,
		Process:    info,
		CompatAddr: compatAddr,
		Connected:  sess != nil,
	}
}

func (r *Runtime) PID() int {
	r.mu.RLock()
	proc := r.process
	r.mu.RUnlock()
	if proc == nil {
		return 0
	}
	return proc.PID()
}

func (r *Runtime) Connected() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.session != nil
}

type ConnectOptions struct {
	Addr     string
	Interval time.Duration
	Codec    ipc.Codec
}

// Connect dials the game-owned compat endpoint. It retries until ctx expires.
func (r *Runtime) Connect(ctx context.Context, opts ConnectOptions) error {
	addr := opts.Addr
	if addr == "" {
		r.mu.RLock()
		addr = r.compatAddr
		r.mu.RUnlock()
	}
	if addr == "" {
		return fmt.Errorf("noderuntime: compat address is required")
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}

	codec := opts.Codec
	if codec == nil {
		r.mu.RLock()
		codec = r.codec
		r.mu.RUnlock()
	}

	var lastErr error
	for {
		sess, err := ipc.Dial(ctx, addr, codec)
		if err == nil {
			r.mu.Lock()
			old := r.session
			r.session = sess
			r.compatAddr = addr
			r.mu.Unlock()
			if old != nil {
				_ = old.Close()
			}
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("noderuntime: connect to compat endpoint %q failed: %w", addr, lastErr)
			}
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (r *Runtime) Request(ctx context.Context, command string, payload any, out any) error {
	r.mu.RLock()
	sess := r.session
	r.mu.RUnlock()
	if sess == nil {
		return ErrNoSession
	}
	return sess.Request(ctx, command, payload, out)
}

func (r *Runtime) Notify(ctx context.Context, command string, payload any) error {
	r.mu.RLock()
	sess := r.session
	r.mu.RUnlock()
	if sess == nil {
		return ErrNoSession
	}
	return sess.Notify(ctx, command, payload)
}

func (r *Runtime) Events() <-chan ipc.Envelope {
	r.mu.RLock()
	sess := r.session
	r.mu.RUnlock()
	if sess == nil {
		ch := make(chan ipc.Envelope)
		close(ch)
		return ch
	}
	return sess.Events()
}

func (r *Runtime) Wait(ctx context.Context) (ipc.ProcessInfo, error) {
	r.mu.RLock()
	proc := r.process
	r.mu.RUnlock()
	if proc == nil {
		return ipc.ProcessInfo{}, ErrNoProcess
	}
	return proc.Wait(ctx)
}

func (r *Runtime) Kill() error {
	r.mu.RLock()
	proc := r.process
	r.mu.RUnlock()
	if proc == nil {
		return ErrNoProcess
	}
	return proc.Kill()
}

// Stop optionally sends a one-way compat command, waits for the process to exit,
// then kills it if configured to do so. It does not assume any game-specific
// shutdown command unless the caller supplies one.
func (r *Runtime) Stop(ctx context.Context, opts StopOptions) (ipc.ProcessInfo, error) {
	r.mu.RLock()
	proc := r.process
	sess := r.session
	r.mu.RUnlock()
	if proc == nil {
		return ipc.ProcessInfo{}, ErrNoProcess
	}

	if opts.Command == "" {
		if err := proc.Kill(); err != nil {
			return proc.Info(), err
		}
		return proc.Wait(ctx)
	}

	if opts.GracePeriod <= 0 {
		opts.GracePeriod = 5 * time.Second
	}

	killAfterGrace := !opts.NoKillAfterGrace

	if sess != nil {
		_ = sess.Notify(ctx, opts.Command, opts.Payload)
	}

	waitCtx, cancel := context.WithTimeout(ctx, opts.GracePeriod)
	defer cancel()

	info, err := proc.Wait(waitCtx)
	if err == nil {
		return info, nil
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return info, err
	}
	if !killAfterGrace {
		return info, err
	}
	if killErr := proc.Kill(); killErr != nil {
		return proc.Info(), killErr
	}
	return proc.Wait(ctx)
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	sess := r.session
	r.session = nil
	r.mu.Unlock()
	if sess != nil {
		return sess.Close()
	}
	return nil
}
