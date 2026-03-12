package snapshot

import (
	"context"
	"fmt"
	"log"
	"os"
)

// Manager handles snapshot lifecycle: build, push, pull.
type Manager struct {
	store    SnapshotStore
	buildDir string // scratch directory for building snapshots
}

// NewManager creates a Manager with the given store and build directory.
func NewManager(store SnapshotStore, buildDir string) *Manager {
	return &Manager{store: store, buildDir: buildDir}
}

// Build creates a Firecracker snapshot from a running VM.
//
// This is a stub that calls `firecracker-snapshot` (a helper binary shipped
// with agenkit-runtime) to serialise the VM's memory and disk state.
// The resulting files are placed into a temporary directory and returned.
func (m *Manager) Build(ctx context.Context, vmPID int, snapshotName string) (string, error) {
	dir, err := os.MkdirTemp(m.buildDir, "snapshot-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp snapshot dir: %w", err)
	}

	log.Printf("INFO: building snapshot %s from VM PID %d into %s", snapshotName, vmPID, dir)
	// In a real implementation, this would invoke the Firecracker MMDS or
	// the snapshot API via the Firecracker UDS socket.
	// Placeholder: create a marker file.
	markerPath := dir + "/snapshot.meta"
	if err := os.WriteFile(markerPath, []byte(fmt.Sprintf(`{"vm_pid":%d,"name":"%s"}`, vmPID, snapshotName)), 0644); err != nil {
		return "", fmt.Errorf("failed to write snapshot meta: %w", err)
	}

	return dir, nil
}

// Push uploads a local snapshot directory to the store.
func (m *Manager) Push(ctx context.Context, snapshotName, localDir string) error {
	log.Printf("INFO: pushing snapshot %s from %s", snapshotName, localDir)
	if err := m.store.Push(ctx, snapshotName, localDir); err != nil {
		return fmt.Errorf("failed to push snapshot: %w", err)
	}
	return nil
}

// Pull downloads a snapshot from the store into localDir.
func (m *Manager) Pull(ctx context.Context, snapshotName, localDir string) error {
	log.Printf("INFO: pulling snapshot %s into %s", snapshotName, localDir)
	if err := m.store.Pull(ctx, snapshotName, localDir); err != nil {
		return fmt.Errorf("failed to pull snapshot: %w", err)
	}
	return nil
}
