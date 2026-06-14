package ipc

import (
	"context"
	"fmt"
	"net"
)

type Transport interface {
	Scheme() string
	Listen(ctx context.Context, address string) (net.Listener, error)
	Dial(ctx context.Context, address string) (net.Conn, error)
}

type TCPTransport struct{}

func (TCPTransport) Scheme() string { return "tcp" }

func (TCPTransport) Listen(ctx context.Context, address string) (net.Listener, error) {
	if address == "" {
		address = "127.0.0.1:0"
	}
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp", address)
}

func (TCPTransport) Dial(ctx context.Context, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", address)
}

func TransportByScheme(scheme string) (Transport, error) {
	switch scheme {
	case "", "tcp":
		return TCPTransport{}, nil
	default:
		return nil, fmt.Errorf("ipc: unsupported transport %q", scheme)
	}
}
