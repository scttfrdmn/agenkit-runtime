package migration

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/scttfrdmn/agenkit-runtime/pkg/vsock"
)

// Migrator orchestrates the full spot-migration flow for all sessions running
// on a host that is about to be evicted.
//
// Flow:
//  1. For each active VM slot: open vsock bus, send checkpoint_now signal
//  2. Wait for checkpoint ACKs (deadline-bounded)
//  3. Record checkpoint IDs in the MigrationManifest
//  4. Persist the manifest to the snapshot store
//  5. Signal the scheduler to resume sessions on another host
type Migrator struct {
	// HostAddr is the address of this host (used in MigrationContext.SourceHost).
	HostAddr string
	// VMAddrs maps slot index to its vsock dial address.
	VMAddrs map[int]string
	// MigrationID is the unique ID for this migration event (typically a UUID).
	MigrationID string
	// Reason is the interruption reason ("spot_warning" | "drain" | "user").
	Reason string
}

// MigrateAll sends checkpoint signals to all active VMs and collects their
// checkpoint IDs. It returns a MigrationManifest describing the outcome.
//
// deadline is the hard deadline (e.g. spot termination time) beyond which VMs
// will be force-killed.
func (m *Migrator) MigrateAll(ctx context.Context, activeSessions map[int]string, deadline time.Time) (*MigrationManifest, error) {
	manifest := &MigrationManifest{
		MigrationID:   m.MigrationID,
		SourceHost:    m.HostAddr,
		StartedAt:     time.Now(),
		InterruptedBy: m.Reason,
	}

	remaining := time.Until(deadline)
	if remaining < 10*time.Second {
		remaining = 10 * time.Second
	}
	// Reserve 15s buffer so we don't cut it too close to the termination deadline.
	if remaining > 15*time.Second {
		remaining -= 15 * time.Second
	}

	type checkpointResult struct {
		slotIndex    int
		sessionID    string
		checkpointID string
		err          error
	}

	results := make(chan checkpointResult, len(activeSessions))
	deadlineCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()

	for slot, sessionID := range activeSessions {
		slot, sessionID := slot, sessionID
		vsockAddr, ok := m.VMAddrs[slot]
		if !ok {
			results <- checkpointResult{slot, sessionID, "", fmt.Errorf("no vsock address for slot %d", slot)}
			continue
		}
		go func() {
			bus, err := vsock.Dial(deadlineCtx, vsockAddr, 5*time.Second)
			if err != nil {
				results <- checkpointResult{slot, sessionID, "", fmt.Errorf("vsock dial: %w", err)}
				return
			}
			defer func() { _ = bus.Close() }()

			deadlineSec := int(time.Until(deadline).Seconds())
			cpID, err := bus.RequestCheckpoint(deadlineCtx, m.Reason, m.MigrationID, deadlineSec)
			results <- checkpointResult{slot, sessionID, cpID, err}
		}()
	}

	for i := 0; i < len(activeSessions); i++ {
		r := <-results
		sm := SessionMigration{
			SessionID:    r.sessionID,
			CheckpointID: r.checkpointID,
			Status:       "pending",
		}
		if r.err != nil {
			sm.Error = r.err.Error()
			sm.Status = "failed"
			log.Printf("WARNING: migration: session %s slot %d: %v", r.sessionID, r.slotIndex, r.err)
		} else {
			log.Printf("INFO: migration: session %s checkpointed as %s", r.sessionID, r.checkpointID)
		}
		manifest.Sessions = append(manifest.Sessions, sm)
	}

	return manifest, nil
}
