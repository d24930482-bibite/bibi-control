package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

type Session struct {
	conn  net.Conn
	codec Codec

	enc *json.Encoder
	dec *json.Decoder

	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool

	pendingMu sync.Mutex
	pending   map[string]chan Envelope

	events chan Envelope
}

func Dial(ctx context.Context, addr string, codec Codec) (*Session, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return NewSession(conn, codec), nil
}

func NewSession(conn net.Conn, codec Codec) *Session {
	if codec == nil {
		codec = DefaultCodec()
	}
	s := &Session{
		conn:    conn,
		codec:   codec,
		enc:     json.NewEncoder(conn),
		dec:     json.NewDecoder(conn),
		pending: make(map[string]chan Envelope),
		events:  make(chan Envelope, 128),
	}
	go s.readLoop()
	return s
}

func (s *Session) Events() <-chan Envelope { return s.events }

func (s *Session) Notify(ctx context.Context, command string, payload any) error {
	env, err := s.newEnvelope(KindEvent, command, payload)
	if err != nil {
		return err
	}
	return s.Send(ctx, env)
}

func (s *Session) Request(ctx context.Context, command string, payload any, out any) error {
	env, err := s.newEnvelope(KindRequest, command, payload)
	if err != nil {
		return err
	}
	if env.ID == "" {
		env.ID = newID()
	}

	ch := make(chan Envelope, 1)
	s.pendingMu.Lock()
	s.pending[env.ID] = ch
	s.pendingMu.Unlock()

	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, env.ID)
		s.pendingMu.Unlock()
	}()

	if err := s.Send(ctx, env); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case reply, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if reply.Error != "" {
			return fmt.Errorf("%w: %s", ErrRequestFailed, reply.Error)
		}
		return s.codec.Unmarshal(reply.Payload, out)
	}
}

func (s *Session) Send(ctx context.Context, env Envelope) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if env.ID == "" {
		env.ID = newID()
	}
	if env.Time.IsZero() {
		env.Time = time.Now().UTC()
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.closeMu.Lock()
	closed := s.closed
	s.closeMu.Unlock()
	if closed {
		return ErrClosed
	}

	return s.enc.Encode(env)
}

func (s *Session) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return nil
	}
	s.closed = true
	s.closeMu.Unlock()
	return s.conn.Close()
}

func (s *Session) newEnvelope(kind MessageKind, command string, payload any) (Envelope, error) {
	raw, err := s.codec.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		ID:      newID(),
		Kind:    kind,
		Command: command,
		Payload: raw,
		Time:    time.Now().UTC(),
	}, nil
}

func (s *Session) readLoop() {
	defer close(s.events)
	defer s.failPending()

	for {
		var env Envelope
		if err := s.dec.Decode(&env); err != nil {
			_ = s.Close()
			return
		}
		if env.Time.IsZero() {
			env.Time = time.Now().UTC()
		}

		if env.ReplyTo != "" {
			s.pendingMu.Lock()
			ch := s.pending[env.ReplyTo]
			s.pendingMu.Unlock()
			if ch != nil {
				select {
				case ch <- env:
				default:
				}
				continue
			}
		}

		select {
		case s.events <- env:
		default:
			// Drop unsolicited events if the caller is not consuming them.
		}
	}
}

func (s *Session) failPending() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
}
