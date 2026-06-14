package ipc

import (
	"encoding/json"
	"io"
	"net"
	"sync"
	"time"
)

const ProtocolVersion = 1

type Envelope struct {
	Version     int         `json:"v"`
	ID          string      `json:"id,omitempty"`
	ReplyTo     string      `json:"reply_to,omitempty"`
	Kind        MessageKind `json:"kind"`
	ProcessID   string      `json:"process_id,omitempty"`
	Token       string      `json:"token,omitempty"`
	Command     string      `json:"command,omitempty"`
	ContentType string      `json:"content_type,omitempty"`
	Payload     []byte      `json:"payload,omitempty"`
	Error       string      `json:"error,omitempty"`
	Time        time.Time   `json:"time"`
}

type Protocol struct {
	Serializer Serializer
}

func NewProtocol(serializer Serializer) Protocol {
	if serializer == nil {
		serializer = DefaultSerializer()
	}
	return Protocol{Serializer: serializer}
}

type Peer struct {
	conn     net.Conn
	enc      *json.Encoder
	dec      *json.Decoder
	protocol Protocol
	writeMu  sync.Mutex
}

func NewPeer(conn net.Conn, protocol Protocol) *Peer {
	return &Peer{
		conn:     conn,
		enc:      json.NewEncoder(conn),
		dec:      json.NewDecoder(conn),
		protocol: protocol,
	}
}

func (p *Peer) Conn() net.Conn { return p.conn }

func (p *Peer) Close() error { return p.conn.Close() }

func (p *Peer) Send(kind MessageKind, processID, token, command string, payload any) error {
	raw, err := p.protocol.Serializer.Marshal(payload)
	if err != nil {
		return err
	}
	return p.SendEnvelope(Envelope{
		Version:     ProtocolVersion,
		ID:          newID(),
		Kind:        kind,
		ProcessID:   processID,
		Token:       token,
		Command:     command,
		ContentType: p.protocol.Serializer.ContentType(),
		Payload:     raw,
		Time:        time.Now(),
	})
}

func (p *Peer) SendReply(replyTo, processID, token string, payload any, replyErr error) error {
	raw, err := p.protocol.Serializer.Marshal(payload)
	if err != nil {
		return err
	}
	env := Envelope{
		Version:     ProtocolVersion,
		ID:          newID(),
		ReplyTo:     replyTo,
		Kind:        KindResponse,
		ProcessID:   processID,
		Token:       token,
		ContentType: p.protocol.Serializer.ContentType(),
		Payload:     raw,
		Time:        time.Now(),
	}
	if replyErr != nil {
		env.Kind = KindError
		env.Error = replyErr.Error()
	}
	return p.SendEnvelope(env)
}

func (p *Peer) SendEnvelope(env Envelope) error {
	if env.Version == 0 {
		env.Version = ProtocolVersion
	}
	if env.ID == "" {
		env.ID = newID()
	}
	if env.Time.IsZero() {
		env.Time = time.Now()
	}
	if env.ContentType == "" && len(env.Payload) > 0 {
		env.ContentType = p.protocol.Serializer.ContentType()
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.enc.Encode(env)
}

func (p *Peer) Receive() (Envelope, error) {
	var env Envelope
	if err := p.dec.Decode(&env); err != nil {
		return Envelope{}, err
	}
	if env.Time.IsZero() {
		env.Time = time.Now()
	}
	return env, nil
}

func (p *Peer) DecodePayload(env Envelope, out any) error {
	return p.protocol.Serializer.Unmarshal(env.Payload, out)
}

func IsClosedConn(err error) bool {
	return err == io.EOF || err == net.ErrClosed
}
