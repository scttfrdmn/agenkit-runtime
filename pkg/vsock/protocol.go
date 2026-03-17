// Package vsock defines the host↔guest vsock signalling protocol.
//
// The host sends a HostSignal (JSON, newline-terminated) over a vsock connection
// to a guest agent. The guest acknowledges with a GuestAck.
package vsock

// SignalType identifies the action the host is requesting.
type SignalType string

const (
	// SignalCheckpointNow asks the guest to immediately create a checkpoint
	// and report back its ID.
	SignalCheckpointNow SignalType = "checkpoint_now"
	// SignalShutdown asks the guest to perform an orderly shutdown.
	SignalShutdown SignalType = "shutdown"
	// SignalResumeMigrated asks a fresh guest to resume a previously
	// checkpointed session identified by CheckpointID.
	SignalResumeMigrated SignalType = "resume_migrated"
)

// HostSignal is sent by the host to a running guest agent.
type HostSignal struct {
	// Type is the requested action.
	Type SignalType `json:"type"`
	// Reason explains why the signal was sent.
	// Valid values: "spot_warning" | "drain" | "user" | "migration"
	Reason string `json:"reason"`
	// DeadlineSec, if > 0, is the maximum number of seconds the guest has
	// to comply before the host force-kills the VM.
	DeadlineSec int `json:"deadline_sec"`
	// MigrationID is a unique identifier for this migration event, copied into
	// the checkpoint's MigrationContext so the receiving host can correlate.
	MigrationID string `json:"migration_id"`
	// CheckpointID identifies the checkpoint the guest should restore when
	// handling a SignalResumeMigrated signal. Empty for other signal types.
	CheckpointID string `json:"checkpoint_id,omitempty"`
}

// GuestAck is sent by the guest in response to a HostSignal.
type GuestAck struct {
	// Type echoes the signal type with an "_ack" suffix (e.g. "checkpoint_ack").
	Type string `json:"type"`
	// CheckpointID is populated when responding to a SignalCheckpointNow.
	CheckpointID string `json:"checkpoint_id,omitempty"`
	// Error, if non-empty, indicates that the guest failed to comply.
	Error string `json:"error,omitempty"`
}
