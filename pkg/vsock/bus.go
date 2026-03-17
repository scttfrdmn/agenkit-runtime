package vsock

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Bus manages a single vsock connection between the host and a guest VM.
//
// The host dials the guest's vsock listener on the well-known port.
// Messages are JSON-encoded, newline-delimited.
type Bus struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
}

// Dial opens a vsock connection to the guest identified by cid on vsockPort.
//
// On Linux with the vsock driver available, addr should be the vsock CID of the
// guest (as a host:port pair where host is the CID decimal). For development
// outside a hypervisor environment, a TCP address can be used instead.
func Dial(ctx context.Context, addr string, timeout time.Duration) (*Bus, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("vsock dial %s: %w", addr, err)
	}
	return newBus(conn), nil
}

func newBus(conn net.Conn) *Bus {
	r := bufio.NewReader(conn)
	return &Bus{
		conn: conn,
		enc:  json.NewEncoder(conn),
		dec:  json.NewDecoder(r),
	}
}

// SendSignal sends a HostSignal to the guest.
func (b *Bus) SendSignal(sig HostSignal) error {
	if err := b.enc.Encode(sig); err != nil {
		return fmt.Errorf("vsock send signal: %w", err)
	}
	return nil
}

// ReceiveAck waits for a GuestAck from the guest.
func (b *Bus) ReceiveAck() (*GuestAck, error) {
	var ack GuestAck
	if err := b.dec.Decode(&ack); err != nil {
		return nil, fmt.Errorf("vsock receive ack: %w", err)
	}
	return &ack, nil
}

// RequestCheckpoint sends a checkpoint_now signal and waits for the ack.
// It returns the checkpoint ID reported by the guest.
func (b *Bus) RequestCheckpoint(ctx context.Context, reason, migrationID string, deadlineSec int) (string, error) {
	sig := HostSignal{
		Type:        SignalCheckpointNow,
		Reason:      reason,
		DeadlineSec: deadlineSec,
		MigrationID: migrationID,
	}
	if err := b.SendSignal(sig); err != nil {
		return "", err
	}

	type ackResult struct {
		ack *GuestAck
		err error
	}
	ch := make(chan ackResult, 1)
	go func() {
		ack, err := b.ReceiveAck()
		ch <- ackResult{ack, err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return "", r.err
		}
		if r.ack.Error != "" {
			return "", fmt.Errorf("guest checkpoint error: %s", r.ack.Error)
		}
		return r.ack.CheckpointID, nil
	}
}

// RequestResume sends a resume_migrated signal and waits for the ack.
// checkpointID identifies the checkpoint the guest should restore.
// Returns nil on success (ack received without error).
func (b *Bus) RequestResume(ctx context.Context, checkpointID, migrationID string, deadlineSec int) error {
	sig := HostSignal{
		Type:         SignalResumeMigrated,
		Reason:       "migration",
		CheckpointID: checkpointID,
		MigrationID:  migrationID,
		DeadlineSec:  deadlineSec,
	}
	if err := b.SendSignal(sig); err != nil {
		return err
	}

	type ackResult struct {
		ack *GuestAck
		err error
	}
	ch := make(chan ackResult, 1)
	go func() {
		ack, err := b.ReceiveAck()
		ch <- ackResult{ack, err}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if r.ack.Error != "" {
			return fmt.Errorf("guest resume error: %s", r.ack.Error)
		}
		return nil
	}
}

// Close closes the underlying connection.
func (b *Bus) Close() error { return b.conn.Close() }
