package migration

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startFakeVsock starts a TCP listener that reads one JSON line and writes back resp.
// Returns the listener address.
func startFakeVsock(t *testing.T, resp string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Read the incoming signal (discard it).
		scanner := bufio.NewScanner(conn)
		scanner.Scan()
		// Write back the canned response.
		_, _ = conn.Write([]byte(resp + "\n"))
	}()

	return ln.Addr().String()
}

func newMigrator(t *testing.T, addrs map[int]string, manifestDir string) *Migrator {
	t.Helper()
	return &Migrator{
		HostAddr:    "test-host",
		VMAddrs:     addrs,
		MigrationID: "test-migration-001",
		Reason:      "spot_warning",
		ManifestDir: manifestDir,
	}
}

func TestMigrateAllSuccess(t *testing.T) {
	addr := startFakeVsock(t, `{"type":"checkpoint_ack","checkpoint_id":"chk-123"}`)

	m := newMigrator(t, map[int]string{0: addr}, "")
	deadline := time.Now().Add(2 * time.Minute)

	manifest, err := m.MigrateAll(context.Background(), map[int]string{0: "session-1"}, deadline)
	if err != nil {
		t.Fatalf("MigrateAll: %v", err)
	}
	if len(manifest.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(manifest.Sessions))
	}
	s := manifest.Sessions[0]
	if s.Status != "pending" {
		t.Fatalf("expected pending, got %s", s.Status)
	}
	if s.CheckpointID != "chk-123" {
		t.Fatalf("expected chk-123, got %s", s.CheckpointID)
	}
	if s.SessionID != "session-1" {
		t.Fatalf("expected session-1, got %s", s.SessionID)
	}
}

func TestMigrateAllVsockError(t *testing.T) {
	addr := startFakeVsock(t, `{"type":"checkpoint_ack","error":"OOM"}`)

	m := newMigrator(t, map[int]string{0: addr}, "")
	deadline := time.Now().Add(2 * time.Minute)

	manifest, err := m.MigrateAll(context.Background(), map[int]string{0: "session-err"}, deadline)
	if err != nil {
		t.Fatalf("MigrateAll: %v", err)
	}
	if len(manifest.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(manifest.Sessions))
	}
	s := manifest.Sessions[0]
	if s.Status != "failed" {
		t.Fatalf("expected failed, got %s", s.Status)
	}
	if s.Error == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestMigrateAllManifestPersisted(t *testing.T) {
	addr := startFakeVsock(t, `{"type":"checkpoint_ack","checkpoint_id":"chk-persist"}`)

	dir := t.TempDir()
	m := newMigrator(t, map[int]string{0: addr}, dir)
	deadline := time.Now().Add(2 * time.Minute)

	manifest, err := m.MigrateAll(context.Background(), map[int]string{0: "session-p"}, deadline)
	if err != nil {
		t.Fatalf("MigrateAll: %v", err)
	}

	// Read and unmarshal the persisted manifest file.
	manifestPath := filepath.Join(dir, manifest.MigrationID+".json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest file: %v", err)
	}

	var got MigrationManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got.MigrationID != manifest.MigrationID {
		t.Fatalf("migration ID mismatch: %s vs %s", got.MigrationID, manifest.MigrationID)
	}
	if len(got.Sessions) != 1 {
		t.Fatalf("expected 1 session in persisted manifest, got %d", len(got.Sessions))
	}
}

func TestMigrateAllContextCancel(t *testing.T) {
	// Listener that never responds (simulates a hung guest).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// hold open but never write
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	m := newMigrator(t, map[int]string{0: ln.Addr().String()}, "")
	deadline := time.Now().Add(2 * time.Minute)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.MigrateAll(ctx, map[int]string{0: "session-cancel"}, deadline)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MigrateAll did not return after context cancel")
	}
}
