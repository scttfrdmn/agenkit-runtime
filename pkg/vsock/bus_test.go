package vsock

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// dialTCP dials a TCP address and wraps it in a Bus (same as Dial but avoids the
// net.Dialer overhead in tests).
func dialTCP(t *testing.T, addr string) *Bus {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return newBus(conn)
}

func TestBusRequestCheckpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read the HostSignal.
		var sig HostSignal
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			_ = json.Unmarshal(scanner.Bytes(), &sig)
		}

		// Respond with a checkpoint ack.
		ack := GuestAck{
			Type:         "checkpoint_ack",
			CheckpointID: "chk-abc",
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(ack)
	}()

	bus := dialTCP(t, ln.Addr().String())
	ctx := context.Background()
	cpID, err := bus.RequestCheckpoint(ctx, "spot_warning", "mig-001", 60)
	if err != nil {
		t.Fatalf("RequestCheckpoint: %v", err)
	}
	if cpID != "chk-abc" {
		t.Fatalf("expected chk-abc, got %s", cpID)
	}
}

func TestBusSignalRoundTrip(t *testing.T) {
	original := HostSignal{
		Type:        SignalCheckpointNow,
		Reason:      "drain",
		DeadlineSec: 120,
		MigrationID: "mig-xyz",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded HostSignal
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type: got %s, want %s", decoded.Type, original.Type)
	}
	if decoded.Reason != original.Reason {
		t.Errorf("Reason: got %s, want %s", decoded.Reason, original.Reason)
	}
	if decoded.DeadlineSec != original.DeadlineSec {
		t.Errorf("DeadlineSec: got %d, want %d", decoded.DeadlineSec, original.DeadlineSec)
	}
	if decoded.MigrationID != original.MigrationID {
		t.Errorf("MigrationID: got %s, want %s", decoded.MigrationID, original.MigrationID)
	}
}

func TestBusAckTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Read the signal but never send an ack — simulates a delayed/hung guest.
		scanner := bufio.NewScanner(conn)
		scanner.Scan()
		// hold here indefinitely
		time.Sleep(10 * time.Second)
	}()

	bus := dialTCP(t, ln.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = bus.RequestCheckpoint(ctx, "spot_warning", "mig-timeout", 60)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
