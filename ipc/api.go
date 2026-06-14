package ipc

import (
	"context"
	"fmt"
	"sync"
)

// Controller is the in-process API for code that owns process supervision.
// It treats managed processes as opaque external nodes by default. A node may
// optionally connect back over the protocol; only then are Command/Request
// operations available.
type Controller struct {
	manager *Manager
}

func NewController(manager *Manager) *Controller {
	return &Controller{manager: manager}
}

func NewLocalController(ctx context.Context, opts ManagerOptions) (*Controller, error) {
	manager, err := NewManager(ctx, opts)
	if err != nil {
		return nil, err
	}
	return NewController(manager), nil
}

// Backward-compatible names. Prefer Controller in new code.
type API = Controller

func NewAPI(manager *Manager) *Controller { return NewController(manager) }

func NewLocalAPI(ctx context.Context, opts ManagerOptions) (*Controller, error) {
	return NewLocalController(ctx, opts)
}

func (c *Controller) Manager() *Manager { return c.manager }

func (c *Controller) Close() error { return c.manager.Close() }

func (c *Controller) Addr() string { return c.manager.Addr() }

func (c *Controller) TransportScheme() string { return c.manager.TransportScheme() }

func (c *Controller) Events() <-chan Event { return c.manager.Events() }

func (c *Controller) StartNode(ctx context.Context, spec ProcessSpec) (Health, error) {
	return c.manager.Start(ctx, spec)
}

func (c *Controller) StopNode(ctx context.Context, processID string) error {
	return c.manager.Stop(ctx, processID)
}

func (c *Controller) KillNode(processID string) error {
	return c.manager.Kill(processID)
}

func (c *Controller) Health(processID string) (Health, error) {
	return c.manager.Health(processID)
}

func (c *Controller) ListHealth() []Health { return c.manager.ListHealth() }

func (c *Controller) Capabilities(processID string) ([]Capability, error) {
	return c.manager.Capabilities(processID)
}

func (c *Controller) ProtocolConnected(processID string) (bool, error) {
	return c.manager.ProtocolConnected(processID)
}

func (c *Controller) WaitProtocol(ctx context.Context, processID string) error {
	return c.manager.WaitProtocol(ctx, processID)
}

// SendCommand is fire-and-forget and only works for nodes with an active
// protocol connection. Opaque external processes return ErrProtocolUnavailable.
func (c *Controller) SendCommand(processID, command string, payload any) error {
	return c.manager.Send(processID, command, payload)
}

// Request sends a command and waits for a reply. It only works for nodes with an
// active protocol connection. Opaque external processes return
// ErrProtocolUnavailable.
func (c *Controller) Request(ctx context.Context, processID, command string, payload any, out any) error {
	return c.manager.Call(ctx, processID, command, payload, out)
}

// Backward-compatible process-oriented names. Prefer Node terminology in new code.
func (c *Controller) StartProcess(ctx context.Context, spec ProcessSpec) (Health, error) {
	return c.StartNode(ctx, spec)
}

func (c *Controller) StopProcess(ctx context.Context, processID string) error {
	return c.StopNode(ctx, processID)
}

// CommandRequest is the node-side representation of a command received from the
// controller. It is only used by nodes that opt into the protocol.
type CommandRequest struct {
	Envelope Envelope
	node     *NodeClient
}

func (r CommandRequest) ProcessID() string { return r.Envelope.ProcessID }

func (r CommandRequest) Command() string { return r.Envelope.Command }

func (r CommandRequest) Decode(out any) error {
	return r.node.client.DecodePayload(r.Envelope, out)
}

type CommandHandler func(context.Context, CommandRequest) (any, error)

// NodeClient is the optional node-side SDK. External processes do not need it.
// Use it only for helper processes or injected/cooperative nodes that can speak
// the control protocol.
type NodeClient struct {
	client   *Client
	mu       sync.RWMutex
	handlers map[string]CommandHandler
}

func NewNodeClient(client *Client) *NodeClient {
	return &NodeClient{
		client:   client,
		handlers: make(map[string]CommandHandler),
	}
}

func DialNodeClientFromEnv(ctx context.Context) (*NodeClient, error) {
	client, err := DialFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	return NewNodeClient(client), nil
}

// Backward-compatible names. Prefer NodeClient in new code.
type ProcessAPI = NodeClient

func NewProcessAPI(client *Client) *NodeClient { return NewNodeClient(client) }

func DialProcessAPIFromEnv(ctx context.Context) (*NodeClient, error) {
	return DialNodeClientFromEnv(ctx)
}

func (n *NodeClient) Close() error { return n.client.Close() }

func (n *NodeClient) Heartbeat(payload any) error { return n.client.Heartbeat(payload) }

func (n *NodeClient) Event(name string, payload any) error { return n.client.Event(name, payload) }

func (n *NodeClient) Error(message string, payload any) error {
	return n.client.Error(message, payload)
}

func (n *NodeClient) Handle(command string, handler CommandHandler) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.handlers[command] = handler
}

func (n *NodeClient) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		for {
			env, err := n.client.peer.Receive()
			if err != nil {
				errCh <- err
				return
			}
			if env.Kind != KindCommand {
				continue
			}
			if err := n.handleCommand(ctx, env); err != nil {
				errCh <- err
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (n *NodeClient) handleCommand(ctx context.Context, env Envelope) error {
	n.mu.RLock()
	handler := n.handlers[env.Command]
	n.mu.RUnlock()

	if handler == nil {
		return n.client.peer.SendReply(env.ID, n.client.processID, n.client.token, nil, fmt.Errorf("ipc: unknown command %q", env.Command))
	}

	result, err := handler(ctx, CommandRequest{Envelope: env, node: n})
	return n.client.peer.SendReply(env.ID, n.client.processID, n.client.token, result, err)
}
