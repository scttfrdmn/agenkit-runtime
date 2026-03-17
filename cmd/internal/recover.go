package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/migration"
	"github.com/scttfrdmn/agenkit-runtime/pkg/pool"
	"github.com/scttfrdmn/agenkit-runtime/pkg/vsock"
)

func recoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recover",
		Short: "Recover agent sessions from a previous crash or spot eviction",
		Long: `recover reads migration manifests from the snapshot store, identifies
sessions that were interrupted but not yet resumed, and attempts to resume
each one by sending a resume_migrated vsock signal to an available pool slot.

Sessions that cannot reach a ready VM slot are marked "failed" with a
descriptive error. No sessions are silently discarded.

Returns exit code 1 if any sessions could not be recovered.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecover()
		},
	}
}

func runRecover() error {
	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		// Non-fatal: fall back to the default snapshot directory.
		log.Printf("WARNING: recover: failed to load config (%v), using default snapshot dir", err)
		cfg = &config.ClusterConfig{SnapshotStore: "/var/lib/agenkit/snapshots"}
	}

	snapshotDir := cfg.SnapshotStore
	if snapshotDir == "" {
		snapshotDir = "/var/lib/agenkit/snapshots"
	}
	manifestDir := filepath.Join(snapshotDir, "manifests")

	entries, err := os.ReadDir(manifestDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Recovery complete. No pending migrations found.")
			return nil
		}
		return fmt.Errorf("failed to read manifest dir %s: %w", manifestDir, err)
	}

	var manifests []*migration.MigrationManifest
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(manifestDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("WARNING: recover: failed to read %s: %v", path, err)
			continue
		}
		var m migration.MigrationManifest
		if err := json.Unmarshal(data, &m); err != nil {
			log.Printf("WARNING: recover: failed to parse %s: %v", path, err)
			continue
		}
		manifests = append(manifests, &m)
	}

	if len(manifests) == 0 {
		fmt.Println("Recovery complete. No pending migrations found.")
		return nil
	}

	// Build a local pool from cluster state so we can acquire ready VM slots.
	localPool := buildLocalPool()

	failed := 0

	// Print header.
	fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
		"MIGRATION ID", "SESSION ID", "CHECKPOINT ID", "STATUS")
	fmt.Println(strings.Repeat("-", 115))

	for _, m := range manifests {
		for i, s := range m.Sessions {
			if s.Status != "pending" {
				fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
					m.MigrationID, s.SessionID, s.CheckpointID, s.Status)
				continue
			}

			// Attempt to resume the session via a vsock signal to a ready VM slot.
			resumeErr := resumeSession(localPool, m.MigrationID, s.SessionID, s.CheckpointID)
			if resumeErr != nil {
				m.Sessions[i].Status = "failed"
				m.Sessions[i].Error = resumeErr.Error()
				failed++
				log.Printf("WARNING: recover: session %s failed: %v", s.SessionID, resumeErr)
				fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
					m.MigrationID, s.SessionID, s.CheckpointID, "failed")
			} else {
				m.Sessions[i].Status = "resumed"
				m.Sessions[i].ResumedAt = time.Now()
				fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
					m.MigrationID, s.SessionID, s.CheckpointID, "resumed")
			}

			// Rewrite manifest atomically to record the updated status.
			if err := rewriteManifest(manifestDir, m); err != nil {
				log.Printf("WARNING: recover: failed to rewrite manifest %s: %v", m.MigrationID, err)
			}
		}
	}

	fmt.Println(strings.Repeat("-", 115))

	if failed > 0 {
		fmt.Printf("\n%d session(s) could not be recovered. See log output above for details.\n", failed)
		return fmt.Errorf("%d session(s) could not be recovered", failed)
	}

	fmt.Println("\nRecovery complete.")
	return nil
}

// buildLocalPool constructs a pool from the persisted cluster state so the
// recover command can acquire ready VM slots without connecting to the daemon.
// Slots that were in the "ready" state at last shutdown are pre-warmed.
func buildLocalPool() *pool.Pool {
	state, err := config.LoadState(config.DefaultStatePath)
	if err != nil || len(state.Hosts) == 0 {
		// Return an empty single-slot pool; all acquire attempts will fail with a
		// clear "no VMs available" error rather than panicking.
		return pool.NewPool(1)
	}
	// Use the first known host's pool (standalone recover operates locally).
	for _, hs := range state.Hosts {
		p := pool.NewPool(hs.PoolSize)
		for i, stateStr := range hs.VMStates {
			if i >= p.Size() {
				break
			}
			if pool.VMState(stateStr) != pool.VMStateReady {
				continue
			}
			if err := p.VM(i).Provision(0); err != nil {
				continue
			}
			if err := p.VM(i).MarkReady(); err != nil {
				continue
			}
		}
		return p
	}
	return pool.NewPool(1)
}

// vsockAddrForSlot returns the TCP address used to reach the guest vsock agent
// on the given slot. The guest CID is slot+3; the port is the standard
// vsock-over-TCP forward port (2222 + slot) used in development environments.
//
// On production Linux hosts with AF_VSOCK support, the actual vsock CID dialing
// would bypass TCP entirely; this fallback allows testing on macOS and CI.
func vsockAddrForSlot(slotIndex int) string {
	return fmt.Sprintf("127.0.0.1:%d", 2222+slotIndex)
}

// resumeSession acquires a ready pool slot and sends a resume_migrated vsock
// signal to the guest, asking it to restore the given checkpoint.
func resumeSession(p *pool.Pool, migrationID, sessionID, checkpointID string) error {
	vm, err := p.Acquire(sessionID)
	if err != nil {
		return fmt.Errorf("no available pool slots for session %s: %w", sessionID, err)
	}

	addr := vsockAddrForSlot(vm.SlotIndex())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bus, err := vsock.Dial(ctx, addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("vsock dial slot %d (%s): %w", vm.SlotIndex(), addr, err)
	}
	defer func() { _ = bus.Close() }()

	if err := bus.RequestResume(ctx, checkpointID, migrationID, 60); err != nil {
		return fmt.Errorf("resume signal to slot %d: %w", vm.SlotIndex(), err)
	}
	return nil
}

// rewriteManifest atomically rewrites m to dir/{migration_id}.json.
func rewriteManifest(dir string, m *migration.MigrationManifest) error {
	m.CompletedAt = time.Now()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	tmp := filepath.Join(dir, m.MigrationID+".json.tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write manifest tmp: %w", err)
	}
	dest := filepath.Join(dir, m.MigrationID+".json")
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename manifest: %w", err)
	}
	return nil
}
