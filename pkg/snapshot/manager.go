package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// Manager handles snapshot lifecycle: build, push, pull.
type Manager struct {
	store          SnapshotStore
	buildDir       string // scratch directory for building snapshots
	socketPathFn   func(vmPID int) string
}

// defaultSocketPath returns the Firecracker UDS path for a given VM PID.
func defaultSocketPath(vmPID int) string {
	return fmt.Sprintf("/run/firecracker-%d.sock", vmPID)
}

// NewManager creates a Manager with the given store and build directory.
// The Firecracker Unix socket path defaults to /run/firecracker-{pid}.sock.
func NewManager(store SnapshotStore, buildDir string) *Manager {
	return &Manager{store: store, buildDir: buildDir, socketPathFn: defaultSocketPath}
}

// NewManagerWithSocket creates a Manager with an explicit socket path function.
// This is useful in tests or when the socket lives at a non-default path.
func NewManagerWithSocket(store SnapshotStore, buildDir string, socketPathFn func(vmPID int) string) *Manager {
	if socketPathFn == nil {
		socketPathFn = defaultSocketPath
	}
	return &Manager{store: store, buildDir: buildDir, socketPathFn: socketPathFn}
}

// firecrackerClient returns an http.Client that dials the Firecracker Unix socket
// for the given VM PID.
func (m *Manager) firecrackerClient(socketPath string) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport}
}

// firecrackerFaultMessage is the JSON error body returned by Firecracker on failure.
type firecrackerFaultMessage struct {
	FaultMessage string `json:"fault_message"`
}

// fcRequest sends an HTTP request to the Firecracker API at the given path
// and returns an error if the response is not 204 No Content.
func fcRequest(ctx context.Context, client *http.Client, method, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("firecracker api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	raw, _ := io.ReadAll(resp.Body)
	var fault firecrackerFaultMessage
	if jsonErr := json.Unmarshal(raw, &fault); jsonErr == nil && fault.FaultMessage != "" {
		return fmt.Errorf("firecracker api %s %s: status %d: %s", method, path, resp.StatusCode, fault.FaultMessage)
	}
	return fmt.Errorf("firecracker api %s %s: unexpected status %d", method, path, resp.StatusCode)
}

// Build creates a Firecracker snapshot from a running VM by calling the
// Firecracker Management API over the per-VM Unix socket.
//
// Sequence:
//  1. PATCH /vm {"state":"Paused"} — pause the VM
//  2. PUT /snapshot/create — write snapshot + mem files to snapshotDir
//  3. PATCH /vm {"state":"Resumed"} — resume the VM
//
// The VM is always resumed even when the snapshot step fails.
// Returns the local directory containing vm.snap and vm.mem on success.
func (m *Manager) Build(ctx context.Context, vmPID int, snapshotName string) (string, error) {
	socketPath := m.socketPathFn(vmPID)
	snapshotDir := filepath.Join(m.buildDir, snapshotName)

	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create snapshot dir %s: %w", snapshotDir, err)
	}

	log.Printf("INFO: building snapshot %s from VM PID %d (socket %s)", snapshotName, vmPID, socketPath)

	client := m.firecrackerClient(socketPath)

	// Step 1: pause VM.
	pauseBody := []byte(`{"state":"Paused"}`)
	if err := fcRequest(ctx, client, http.MethodPatch, "/vm", pauseBody); err != nil {
		return "", fmt.Errorf("failed to pause VM %d: %w", vmPID, err)
	}

	// Steps 2+3: create snapshot, then always resume.
	snapPath := filepath.Join(snapshotDir, "vm.snap")
	memPath := filepath.Join(snapshotDir, "vm.mem")
	createPayload, err := json.Marshal(map[string]string{
		"snapshot_path": snapPath,
		"mem_file_path": memPath,
		"snapshot_type": "Full",
	})
	if err != nil {
		// Should never happen with a static map.
		_ = fcRequest(ctx, client, http.MethodPatch, "/vm", []byte(`{"state":"Resumed"}`))
		return "", fmt.Errorf("failed to marshal snapshot create request: %w", err)
	}

	var buildErr error
	if buildErr = fcRequest(ctx, client, http.MethodPut, "/snapshot/create", createPayload); buildErr != nil {
		buildErr = fmt.Errorf("failed to create snapshot for VM %d: %w", vmPID, buildErr)
	}

	// Step 3: always attempt resume regardless of snapshot outcome.
	resumeBody := []byte(`{"state":"Resumed"}`)
	if resumeErr := fcRequest(ctx, client, http.MethodPatch, "/vm", resumeBody); resumeErr != nil {
		log.Printf("WARNING: failed to resume VM %d after snapshot: %v", vmPID, resumeErr)
		if buildErr != nil {
			return "", buildErr
		}
		return "", fmt.Errorf("snapshot created but failed to resume VM %d: %w", vmPID, resumeErr)
	}

	if buildErr != nil {
		return "", buildErr
	}

	log.Printf("INFO: snapshot %s complete: %s", snapshotName, snapshotDir)
	return snapshotDir, nil
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
