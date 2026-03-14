package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// SendRequest dials the daemon socket, sends req, and returns the response.
// The caller is responsible for interpreting Response.Data.
func SendRequest(ctx context.Context, req Request) (*Response, error) {
	return SendRequestTo(ctx, SocketPath, req)
}

// SendRequestTo dials socketPath instead of the default SocketPath.
// Useful for tests that spin up the server on a temporary path.
func SendRequestTo(ctx context.Context, socketPath string, req Request) (*Response, error) {
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon at %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, fmt.Errorf("set write deadline: %w", err)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	return &resp, nil
}
