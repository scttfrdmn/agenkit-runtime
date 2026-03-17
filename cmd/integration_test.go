//go:build integration

// Package main integration tests exercise the full pool lifecycle using a
// MockSpawner so no real Firecracker binary is required.
//
// Run with:
//
//	go test -tags integration ./cmd/...
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scttfrdmn/agenkit-runtime/pkg/pool"
)

// mockSpawn simulates a successful Spawn by directly transitioning the VM
// through provisioned → ready states (no process is started).
func mockSpawn(t *testing.T, vm *pool.VM, socketPath string) {
	t.Helper()
	// Create the socket file so a real Spawn health-check would succeed.
	if err := os.WriteFile(socketPath, []byte{}, 0600); err != nil {
		t.Fatalf("mockSpawn: create socket: %v", err)
	}
	if err := vm.Provision(0); err != nil {
		t.Fatalf("mockSpawn: provision: %v", err)
	}
	if err := vm.MarkReady(); err != nil {
		t.Fatalf("mockSpawn: mark ready: %v", err)
	}
}

// TestLocalClusterLifecycle exercises the full pool lifecycle:
//
//  1. Provision a 2-slot pool using MockSpawner (no Firecracker binary)
//  2. Assign 2 sessions
//  3. Drain gracefully
//  4. Verify all slots reach idle state
//  5. Deprovision all slots
func TestLocalClusterLifecycle(t *testing.T) {
	const poolSize = 2
	p := pool.NewPool(poolSize)
	tempDir := t.TempDir()

	// Step 1: provision all slots via MockSpawner.
	for i := 0; i < poolSize; i++ {
		socketPath := filepath.Join(tempDir, "fc-"+string(rune('0'+i))+".sock")
		mockSpawn(t, p.VM(i), socketPath)
	}

	stats := p.Stats()
	if stats[pool.VMStateReady] != poolSize {
		t.Fatalf("expected %d ready slots after provisioning, got %d", poolSize, stats[pool.VMStateReady])
	}
	if p.Available() != poolSize {
		t.Fatalf("expected %d available, got %d", poolSize, p.Available())
	}

	// Step 2: assign sessions directly to each slot.
	// (Pool.Acquire always picks the first ready slot without changing its state,
	// so direct Assign is used here to control slot assignment precisely.)
	if err := p.VM(0).Assign("session-alpha"); err != nil {
		t.Fatalf("assign slot 0: %v", err)
	}
	if err := p.VM(1).Assign("session-beta"); err != nil {
		t.Fatalf("assign slot 1: %v", err)
	}

	sessions := p.ActiveSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 active sessions, got %d", len(sessions))
	}

	// Step 3: drain gracefully.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Drain(ctx, pool.DrainGraceful)

	// DrainGraceful with deadline=0 completes immediately in a goroutine;
	// give the goroutines a brief moment to complete.
	time.Sleep(50 * time.Millisecond)

	// Step 4: verify idle state.
	stats = p.Stats()
	if stats[pool.VMStateIdle] != poolSize {
		t.Fatalf("expected %d idle slots after drain, got stats: %+v", poolSize, stats)
	}
	// Step 5: deprovision all slots.
	for i := 0; i < poolSize; i++ {
		if err := p.VM(i).Deprovision(); err != nil {
			t.Fatalf("deprovision slot %d: %v", i, err)
		}
	}

	stats = p.Stats()
	if stats[pool.VMStateDeprovisioned] != poolSize {
		t.Fatalf("expected %d deprovisioned, got stats: %+v", poolSize, stats)
	}
}
