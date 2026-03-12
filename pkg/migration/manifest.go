package migration

import "time"

// MigrationManifest records the full state of an in-progress or completed
// migration event so that the recover command can triage incomplete migrations.
type MigrationManifest struct {
	// MigrationID is a UUID that correlates all records for this migration.
	MigrationID string `json:"migration_id" yaml:"migration_id"`
	// SourceHost is the hostname of the evicted machine.
	SourceHost string `json:"source_host" yaml:"source_host"`
	// StartedAt is when the migration was initiated.
	StartedAt time.Time `json:"started_at" yaml:"started_at"`
	// CompletedAt is when all sessions were resumed. Zero if still in progress.
	CompletedAt time.Time `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
	// InterruptedBy describes the trigger: "spot_warning" | "drain" | "crash" | "user".
	InterruptedBy string `json:"interrupted_by" yaml:"interrupted_by"`
	// Sessions lists the individual session migrations within this event.
	Sessions []SessionMigration `json:"sessions" yaml:"sessions"`
}

// SessionMigration records the migration of a single agent session.
type SessionMigration struct {
	// SessionID is the agent session that was migrated.
	SessionID string `json:"session_id" yaml:"session_id"`
	// CheckpointID is the checkpoint created at interruption time.
	CheckpointID string `json:"checkpoint_id" yaml:"checkpoint_id"`
	// TargetHost is the hostname that resumed the session.
	TargetHost string `json:"target_host,omitempty" yaml:"target_host,omitempty"`
	// ResumedAt is when the session was successfully resumed.
	ResumedAt time.Time `json:"resumed_at,omitempty" yaml:"resumed_at,omitempty"`
	// Error holds any error that prevented resumption.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
	// Status is one of "pending" | "resumed" | "failed" | "unrecoverable".
	Status string `json:"status" yaml:"status"`
}
