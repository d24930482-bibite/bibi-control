package ipc

import (
	"encoding/json"
	"time"
)

type ProcessState string

const (
	ProcessStarting ProcessState = "starting"
	ProcessRunning  ProcessState = "running"
	ProcessExited   ProcessState = "exited"
	ProcessFailed   ProcessState = "failed"
)

type ProcessSpec struct {
	Path string
	Args []string
	Dir  string
	Env  map[string]string

	// Stdout and Stderr are optional. When nil, output is discarded.
	Stdout Writer
	Stderr Writer
}

type ProcessInfo struct {
	PID       int          `json:"pid"`
	Path      string       `json:"path"`
	Args      []string     `json:"args,omitempty"`
	Dir       string       `json:"dir,omitempty"`
	State     ProcessState `json:"state"`
	StartedAt time.Time    `json:"started_at"`
	ExitedAt  *time.Time   `json:"exited_at,omitempty"`
	ExitCode  *int         `json:"exit_code,omitempty"`
	Error     string       `json:"error,omitempty"`
}

type MessageKind string

const (
	KindRequest  MessageKind = "request"
	KindResponse MessageKind = "response"
	KindEvent    MessageKind = "event"
	KindError    MessageKind = "error"
)

type Envelope struct {
	ID      string          `json:"id,omitempty"`
	ReplyTo string          `json:"reply_to,omitempty"`
	Kind    MessageKind     `json:"kind"`
	Command string          `json:"command,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
	Time    time.Time       `json:"time"`
}

// Writer is intentionally the small subset of io.Writer used by process output.
type Writer interface {
	Write(p []byte) (int, error)
}
