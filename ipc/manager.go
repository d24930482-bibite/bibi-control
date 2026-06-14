package ipc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

type ManagerOptions struct {
	Address     string
	Transport   Transport
	Serializer  Serializer
	Token       string
	EventBuffer int
}

type Manager struct {
	mu        sync.RWMutex
	procs     map[string]*managedProcess
	transport Transport
	protocol  Protocol
	listener  net.Listener
	addr      string
	token     string
	events    chan Event
	ctx       context.Context
	cancel    context.CancelFunc

	pendingMu sync.Mutex
	pending   map[string]chan Envelope
}

type managedProcess struct {
	spec   ProcessSpec
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.RWMutex
	peer   *Peer
	health Health
}

func NewManager(ctx context.Context, opts ManagerOptions) (*Manager, error) {
	if opts.Transport == nil {
		opts.Transport = TCPTransport{}
	}
	if opts.Serializer == nil {
		opts.Serializer = DefaultSerializer()
	}
	if opts.EventBuffer <= 0 {
		opts.EventBuffer = 256
	}
	if opts.Token == "" {
		token, err := RandomToken(32)
		if err != nil {
			return nil, err
		}
		opts.Token = token
	}

	ln, err := opts.Transport.Listen(ctx, opts.Address)
	if err != nil {
		return nil, err
	}

	mctx, cancel := context.WithCancel(ctx)
	m := &Manager{
		procs:     make(map[string]*managedProcess),
		transport: opts.Transport,
		protocol:  NewProtocol(opts.Serializer),
		listener:  ln,
		addr:      ln.Addr().String(),
		token:     opts.Token,
		events:    make(chan Event, opts.EventBuffer),
		ctx:       mctx,
		cancel:    cancel,
		pending:   make(map[string]chan Envelope),
	}

	go m.acceptLoop()
	return m, nil
}

func (m *Manager) Addr() string { return m.addr }

func (m *Manager) TransportScheme() string { return m.transport.Scheme() }

func (m *Manager) Token() string { return m.token }

func (m *Manager) Events() <-chan Event { return m.events }

func (m *Manager) Close() error {
	m.cancel()
	_ = m.listener.Close()

	m.mu.RLock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var firstErr error
	for _, id := range ids {
		if err := m.Stop(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	m.pendingMu.Lock()
	for id, ch := range m.pending {
		delete(m.pending, id)
		close(ch)
	}
	m.pendingMu.Unlock()

	close(m.events)
	return firstErr
}

func (m *Manager) Start(ctx context.Context, spec ProcessSpec) (Health, error) {
	if spec.Executable == "" {
		return Health{}, errors.New("ipc: executable is required")
	}
	if spec.ID == "" {
		id, err := RandomToken(8)
		if err != nil {
			return Health{}, err
		}
		spec.ID = id
	}
	if spec.Name == "" {
		spec.Name = spec.ID
	}
	if spec.ShutdownTimeout <= 0 {
		spec.ShutdownTimeout = 10 * time.Second
	}
	spec.Protocol = normalizeProtocolMode(spec.Protocol)

	m.mu.Lock()
	if _, exists := m.procs[spec.ID]; exists {
		m.mu.Unlock()
		return Health{}, fmt.Errorf("ipc: process %q already exists", spec.ID)
	}
	m.mu.Unlock()

	procCtx, cancel := context.WithCancel(m.ctx)
	cmd := exec.CommandContext(procCtx, spec.Executable, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = m.envForProcess(spec)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return Health{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return Health{}, err
	}

	p := &managedProcess{
		spec:   spec,
		cmd:    cmd,
		cancel: cancel,
		done:   make(chan struct{}),
		health: Health{
			ID:           spec.ID,
			Name:         spec.Name,
			State:        StateStarting,
			Transport:    m.transport.Scheme(),
			Addr:         m.addr,
			ProtocolMode: spec.Protocol,
			Capabilities: []Capability{CapabilityOSProcess, CapabilityStdout, CapabilityStderr},
		},
	}

	m.mu.Lock()
	m.procs[spec.ID] = p
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.procs, spec.ID)
		m.mu.Unlock()
		return Health{}, err
	}

	now := time.Now()
	p.mu.Lock()
	p.health.PID = cmd.Process.Pid
	p.health.State = StateRunning
	p.health.StartedAt = now
	p.mu.Unlock()

	m.emit(Event{ProcessID: spec.ID, Kind: KindEvent, Time: now, Message: "process started"})
	go m.scanLogs(spec.ID, "stdout", stdout)
	go m.scanLogs(spec.ID, "stderr", stderr)
	go m.waitProcess(spec.ID, p)

	return m.Health(spec.ID)
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	p, ok := m.getProc(id)
	if !ok {
		return fmt.Errorf("ipc: unknown process %q", id)
	}

	p.mu.Lock()
	if p.health.State == StateExited || p.health.State == StateFailed {
		p.mu.Unlock()
		return nil
	}
	p.health.State = StateStopping
	p.mu.Unlock()

	// Cooperative shutdown is only available for protocol-capable nodes.
	if err := m.Send(id, "shutdown", map[string]any{"reason": "manager_stop"}); err != nil && !errors.Is(err, ErrProtocolUnavailable) {
		m.emit(Event{ProcessID: id, Kind: KindError, Time: time.Now(), Error: err.Error(), Message: "shutdown command failed"})
	}

	timeout := p.spec.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	stopCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-p.done:
		return nil
	case <-stopCtx.Done():
		return m.Kill(id)
	}
}

func (m *Manager) Kill(id string) error {
	p, ok := m.getProc(id)
	if !ok {
		return fmt.Errorf("ipc: unknown process %q", id)
	}

	p.cancel()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}

	select {
	case <-p.done:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("ipc: process %q did not exit after kill", id)
	}
}

func (m *Manager) Send(id, command string, payload any) error {
	p, peer, err := m.protocolPeer(id)
	if err != nil {
		return err
	}
	_ = p
	return peer.Send(KindCommand, id, m.token, command, payload)
}

func (m *Manager) Call(ctx context.Context, id, command string, payload any, out any) error {
	_, peer, err := m.protocolPeer(id)
	if err != nil {
		return err
	}

	raw, err := m.protocol.Serializer.Marshal(payload)
	if err != nil {
		return err
	}

	env := Envelope{
		Version:     ProtocolVersion,
		ID:          newID(),
		Kind:        KindCommand,
		ProcessID:   id,
		Token:       m.token,
		Command:     command,
		ContentType: m.protocol.Serializer.ContentType(),
		Payload:     raw,
		Time:        time.Now(),
	}

	ch := make(chan Envelope, 1)
	m.pendingMu.Lock()
	m.pending[env.ID] = ch
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pending, env.ID)
		m.pendingMu.Unlock()
	}()

	if err := peer.SendEnvelope(env); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case reply, ok := <-ch:
		if !ok {
			return errors.New("ipc: manager closed while waiting for response")
		}
		if reply.Error != "" {
			return errors.New(reply.Error)
		}
		if out == nil {
			return nil
		}
		return m.protocol.Serializer.Unmarshal(reply.Payload, out)
	}
}

func (m *Manager) WaitProtocol(ctx context.Context, id string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		p, ok := m.getProc(id)
		if !ok {
			return fmt.Errorf("ipc: unknown process %q", id)
		}

		p.mu.RLock()
		connected := p.peer != nil
		state := p.health.State
		p.mu.RUnlock()

		if connected {
			return nil
		}
		if state == StateExited || state == StateFailed {
			return ErrProcessExited
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) ProtocolConnected(id string) (bool, error) {
	p, ok := m.getProc(id)
	if !ok {
		return false, fmt.Errorf("ipc: unknown process %q", id)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.peer != nil, nil
}

func (m *Manager) Capabilities(id string) ([]Capability, error) {
	h, err := m.Health(id)
	if err != nil {
		return nil, err
	}
	return append([]Capability(nil), h.Capabilities...), nil
}

func (m *Manager) Health(id string) (Health, error) {
	p, ok := m.getProc(id)
	if !ok {
		return Health{}, fmt.Errorf("ipc: unknown process %q", id)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	h := p.health
	h.ProtocolConnected = p.peer != nil
	h.Capabilities = capabilitiesFor(h)
	if !h.StartedAt.IsZero() && h.ExitedAt == nil {
		h.Uptime = time.Since(h.StartedAt)
	}
	return h, nil
}

func (m *Manager) ListHealth() []Health {
	m.mu.RLock()
	procs := make([]*managedProcess, 0, len(m.procs))
	for _, p := range m.procs {
		procs = append(procs, p)
	}
	m.mu.RUnlock()

	out := make([]Health, 0, len(procs))
	for _, p := range procs {
		p.mu.RLock()
		h := p.health
		h.ProtocolConnected = p.peer != nil
		h.Capabilities = capabilitiesFor(h)
		if !h.StartedAt.IsZero() && h.ExitedAt == nil {
			h.Uptime = time.Since(h.StartedAt)
		}
		p.mu.RUnlock()
		out = append(out, h)
	}
	return out
}

func (m *Manager) protocolPeer(id string) (*managedProcess, *Peer, error) {
	p, ok := m.getProc(id)
	if !ok {
		return nil, nil, fmt.Errorf("ipc: unknown process %q", id)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.spec.Protocol == ProtocolDisabled || p.peer == nil {
		return nil, nil, fmt.Errorf("%w: process %q has no active protocol peer", ErrProtocolUnavailable, id)
	}
	return p, p.peer, nil
}

func (m *Manager) getProc(id string) (*managedProcess, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.procs[id]
	return p, ok
}

func (m *Manager) acceptLoop() {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			select {
			case <-m.ctx.Done():
				return
			default:
				m.emit(Event{Kind: KindError, Time: time.Now(), Error: err.Error(), Message: "accept failed"})
				continue
			}
		}
		go m.handleConn(conn)
	}
}

func (m *Manager) handleConn(conn net.Conn) {
	peer := NewPeer(conn, m.protocol)
	hello, err := peer.Receive()
	if err != nil || hello.Kind != KindHello || hello.ProcessID == "" || hello.Token != m.token {
		_ = peer.Close()
		return
	}

	p, ok := m.getProc(hello.ProcessID)
	if !ok {
		_ = peer.Close()
		return
	}

	p.mu.Lock()
	if p.spec.Protocol == ProtocolDisabled {
		p.mu.Unlock()
		_ = peer.Close()
		return
	}
	if p.peer != nil {
		_ = p.peer.Close()
	}
	p.peer = peer
	p.health.LastHeartbeat = time.Now()
	p.health.ProtocolConnected = true
	p.health.Capabilities = capabilitiesFor(p.health)
	p.mu.Unlock()

	m.emit(Event{ProcessID: hello.ProcessID, Kind: KindEvent, Time: time.Now(), Message: "protocol peer connected"})

	for {
		env, err := peer.Receive()
		if err != nil {
			p.mu.Lock()
			if p.peer == peer {
				p.peer = nil
				p.health.ProtocolConnected = false
				p.health.Capabilities = capabilitiesFor(p.health)
			}
			p.mu.Unlock()
			_ = peer.Close()
			if !IsClosedConn(err) {
				m.emit(Event{ProcessID: hello.ProcessID, Kind: KindError, Time: time.Now(), Error: err.Error(), Message: "protocol receive failed"})
			}
			return
		}

		if env.ProcessID == "" {
			env.ProcessID = hello.ProcessID
		}
		if env.Token != "" && env.Token != m.token {
			continue
		}
		if env.ReplyTo != "" && m.resolvePending(env) {
			continue
		}
		if env.Kind == KindHeartbeat {
			p.mu.Lock()
			p.health.LastHeartbeat = env.Time
			p.health.Capabilities = capabilitiesFor(p.health)
			p.mu.Unlock()
		}

		m.emit(Event{ProcessID: env.ProcessID, Kind: env.Kind, Time: env.Time, Message: env.Command, Payload: env.Payload, Error: env.Error})
	}
}

func (m *Manager) resolvePending(env Envelope) bool {
	m.pendingMu.Lock()
	ch, ok := m.pending[env.ReplyTo]
	m.pendingMu.Unlock()
	if !ok {
		return false
	}

	select {
	case ch <- env:
	default:
	}
	return true
}

func (m *Manager) waitProcess(id string, p *managedProcess) {
	err := p.cmd.Wait()
	now := time.Now()

	p.mu.Lock()
	if p.peer != nil {
		_ = p.peer.Close()
		p.peer = nil
	}

	exitCode := 0
	if p.cmd.ProcessState != nil {
		exitCode = p.cmd.ProcessState.ExitCode()
	}
	p.health.ExitedAt = &now
	p.health.ExitCode = &exitCode
	p.health.ProtocolConnected = false
	if err != nil {
		p.health.State = StateFailed
		p.health.LastError = err.Error()
	} else {
		p.health.State = StateExited
	}
	p.health.Capabilities = capabilitiesFor(p.health)
	close(p.done)
	p.mu.Unlock()

	m.emit(Event{ProcessID: id, Kind: KindEvent, Time: now, Message: "process exited"})
}

func (m *Manager) scanLogs(id, stream string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		payload, _ := m.protocol.Serializer.Marshal(map[string]string{"stream": stream, "line": scanner.Text()})
		m.emit(Event{ProcessID: id, Kind: KindLog, Time: time.Now(), Payload: payload})
	}
	if err := scanner.Err(); err != nil {
		m.emit(Event{ProcessID: id, Kind: KindError, Time: time.Now(), Error: err.Error(), Message: "log scan failed"})
	}
}

func (m *Manager) emit(ev Event) {
	select {
	case m.events <- ev:
	default:
	}
}

func (m *Manager) envForProcess(spec ProcessSpec) []string {
	if spec.Protocol == ProtocolDisabled {
		return buildEnv(spec.Env, nil)
	}
	return buildEnv(spec.Env, map[string]string{
		EnvAddr:       m.addr,
		EnvToken:      m.token,
		EnvProcessID:  spec.ID,
		EnvTransport:  m.transport.Scheme(),
		EnvSerializer: m.protocol.Serializer.ContentType(),
	})
}

func buildEnv(extra map[string]string, required map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	for k, v := range required {
		env = append(env, k+"="+v)
	}
	return env
}

func normalizeProtocolMode(mode ProtocolMode) ProtocolMode {
	switch mode {
	case "", ProtocolOptional:
		return ProtocolOptional
	case ProtocolDisabled, ProtocolRequired:
		return mode
	default:
		return ProtocolOptional
	}
}

func capabilitiesFor(h Health) []Capability {
	caps := []Capability{CapabilityOSProcess, CapabilityStdout, CapabilityStderr}
	if h.ProtocolConnected {
		caps = append(caps, CapabilityProtocol, CapabilityCommand)
	}
	if !h.LastHeartbeat.IsZero() {
		caps = append(caps, CapabilityHeartbeat)
	}
	return caps
}
