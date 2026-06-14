package ipc

import "context"

// OpaqueNode is deliberately only an association of a local process and/or a
// network session. It has no policy, workspace, database identity, or lifecycle
// model. Higher layers decide what this means.
type OpaqueNode struct {
	ID      string
	Process *Process
	Session *Session
}

func (n *OpaqueNode) PID() int {
	if n == nil || n.Process == nil {
		return 0
	}
	return n.Process.PID()
}

func (n *OpaqueNode) Request(ctx context.Context, command string, payload any, out any) error {
	if n == nil || n.Session == nil {
		return ErrClosed
	}
	return n.Session.Request(ctx, command, payload, out)
}

func (n *OpaqueNode) Notify(ctx context.Context, command string, payload any) error {
	if n == nil || n.Session == nil {
		return ErrClosed
	}
	return n.Session.Notify(ctx, command, payload)
}

func (n *OpaqueNode) Kill() error {
	if n == nil || n.Process == nil {
		return ErrNoProcess
	}
	return n.Process.Kill()
}

func (n *OpaqueNode) Close() error {
	if n == nil || n.Session == nil {
		return nil
	}
	return n.Session.Close()
}
