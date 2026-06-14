package ipc

import (
	"encoding/json"
	"time"
)

const (
	EnvAddr       = "IPC_ADDR"
	EnvToken      = "IPC_TOKEN"
	EnvProcessID  = "IPC_PROCESS_ID"
	EnvTransport  = "IPC_TRANSPORT"
	EnvSerializer = "IPC_SERIALIZER"
)

type ProcessState string

const (
	StateStarting ProcessState = "starting"
	StateRunning  ProcessState = "running"
	StateStopping ProcessState = "stopping"
	StateExited   ProcessState = "exited"
	StateFailed   ProcessState = "failed"
)

// ProtocolMode describes whether a managed process is expected to connect back
// to the controller protocol. External/opaque processes should use
// ProtocolDisabled or ProtocolOptional.
type ProtocolMode string

const (
	// ProtocolOptional is the default. The controller starts and tracks the OS
	// process even when it never connects back to the protocol listener.
	ProtocolOptional ProtocolMode = "optional"

	// ProtocolDisabled means no protocol environment variables are injected and
	// command/request calls will return ErrProtocolUnavailable.
	ProtocolDisabled ProtocolMode = "disabled"

	// ProtocolRequired means the process is expected to connect back. Start still
	// returns after OS launch; callers can use WaitProtocol when they require the
	// protocol to be online before continuing.
	ProtocolRequired ProtocolMode = "required"
)

type Capability string

const (
	CapabilityOSProcess Capability = "os_process"
	CapabilityStdout    Capability = "stdout"
	CapabilityStderr    Capability = "stderr"
	CapabilityProtocol  Capability = "protocol"
	CapabilityHeartbeat Capability = "heartbeat"
	CapabilityCommand   Capability = "command"
)

type MessageKind string

const (
	KindHello     MessageKind = "hello"
	KindHeartbeat MessageKind = "heartbeat"
	KindCommand   MessageKind = "command"
	KindLog       MessageKind = "log"
	KindEvent     MessageKind = "event"
	KindError     MessageKind = "error"
	KindAck       MessageKind = "ack"
	KindResponse  MessageKind = "response"
)

type ProcessSpec struct {
	ID              string
	Name            string
	Executable      string
	Args            []string
	Dir             string
	Env             map[string]string
	ShutdownTimeout time.Duration

	// Protocol controls whether this process is expected to participate in the
	// controller protocol. The zero value behaves as ProtocolOptional.
	Protocol ProtocolMode
}

type Health struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	PID               int           `json:"pid"`
	State             ProcessState  `json:"state"`
	StartedAt         time.Time     `json:"started_at"`
	ExitedAt          *time.Time    `json:"exited_at,omitempty"`
	ExitCode          *int          `json:"exit_code,omitempty"`
	LastHeartbeat     time.Time     `json:"last_heartbeat,omitempty"`
	LastError         string        `json:"last_error,omitempty"`
	Transport         string        `json:"transport"`
	Addr              string        `json:"addr"`
	ProtocolMode      ProtocolMode  `json:"protocol_mode"`
	ProtocolConnected bool          `json:"protocol_connected"`
	Capabilities      []Capability  `json:"capabilities,omitempty"`
	Uptime            time.Duration `json:"uptime"`
}

type Event struct {
	ProcessID string          `json:"process_id,omitempty"`
	Kind      MessageKind     `json:"kind"`
	Time      time.Time       `json:"time"`
	Message   string          `json:"message,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     string          `json:"error,omitempty"`
}
