package ipc

import (
	"context"
	"fmt"
	"os"
)

type Client struct {
	processID string
	token     string
	peer      *Peer
}

func DialFromEnv(ctx context.Context) (*Client, error) {
	addr := os.Getenv(EnvAddr)
	token := os.Getenv(EnvToken)
	processID := os.Getenv(EnvProcessID)
	scheme := os.Getenv(EnvTransport)

	if addr == "" {
		return nil, fmt.Errorf("ipc: %s is not set", EnvAddr)
	}
	if token == "" {
		return nil, fmt.Errorf("ipc: %s is not set", EnvToken)
	}
	if processID == "" {
		return nil, fmt.Errorf("ipc: %s is not set", EnvProcessID)
	}

	transport, err := TransportByScheme(scheme)
	if err != nil {
		return nil, err
	}
	return Dial(ctx, transport, addr, token, processID, DefaultSerializer())
}

func Dial(ctx context.Context, transport Transport, addr, token, processID string, serializer Serializer) (*Client, error) {
	if transport == nil {
		transport = TCPTransport{}
	}
	conn, err := transport.Dial(ctx, addr)
	if err != nil {
		return nil, err
	}

	c := &Client{
		processID: processID,
		token:     token,
		peer:      NewPeer(conn, NewProtocol(serializer)),
	}

	if err := c.peer.Send(KindHello, processID, token, "", map[string]any{"transport": transport.Scheme()}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() error { return c.peer.Close() }

func (c *Client) Heartbeat(payload any) error {
	return c.peer.Send(KindHeartbeat, c.processID, c.token, "", payload)
}

func (c *Client) Event(name string, payload any) error {
	return c.peer.Send(KindEvent, c.processID, c.token, name, payload)
}

func (c *Client) Error(message string, payload any) error {
	return c.peer.Send(KindError, c.processID, c.token, message, payload)
}

func (c *Client) Send(kind MessageKind, command string, payload any) error {
	return c.peer.Send(kind, c.processID, c.token, command, payload)
}

func (c *Client) ReadCommand() (Envelope, error) {
	for {
		env, err := c.peer.Receive()
		if err != nil {
			return Envelope{}, err
		}
		if env.Kind == KindCommand {
			return env, nil
		}
	}
}

func (c *Client) DecodePayload(env Envelope, out any) error {
	return c.peer.DecodePayload(env, out)
}

func (c *Client) CommandLoop(ctx context.Context, handler func(Envelope) error) error {
	errCh := make(chan error, 1)
	go func() {
		for {
			env, err := c.ReadCommand()
			if err != nil {
				errCh <- err
				return
			}
			if err := handler(env); err != nil {
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
