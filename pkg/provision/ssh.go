// Package provision handles SSH-based host bootstrapping.
package provision

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SSHConn is a thin wrapper around an SSH client connection.
type SSHConn struct {
	client *ssh.Client
}

// Close closes the SSH connection.
func (c *SSHConn) Close() error { return c.client.Close() }

// Run executes a command on the remote host and returns combined stdout+stderr.
func (c *SSHConn) Run(cmd string) (string, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// Dial opens an SSH connection to user@addr using the SSH agent or default key files.
func Dial(ctx context.Context, user, addr string) (*SSHConn, error) {
	authMethods, err := sshAuthMethods()
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // internal fleet only
		Timeout:         15 * time.Second,
	}

	host := addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		host = net.JoinHostPort(addr, "22")
	}

	type result struct {
		client *ssh.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		c, e := ssh.Dial("tcp", host, cfg)
		ch <- result{c, e}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("ssh dial %s: %w", host, r.err)
		}
		return &SSHConn{client: r.client}, nil
	}
}

// WaitForSSH retries Dial until it succeeds or the deadline expires.
func WaitForSSH(ctx context.Context, user, addr string, timeout time.Duration) (*SSHConn, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := Dial(ctx, user, addr)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return nil, fmt.Errorf("timed out waiting for SSH on %s", addr)
}

// sshAuthMethods builds a list of SSH auth methods from the running SSH agent
// and well-known default key files.
func sshAuthMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try SSH agent first.
	if agentSock := os.Getenv("SSH_AUTH_SOCK"); agentSock != "" {
		conn, err := net.Dial("unix", agentSock)
		if err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Fall back to default key files.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return methods, nil
	}
	for _, keyFile := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		path := fmt.Sprintf("%s/.ssh/%s", homeDir, keyFile)
		keyBytes, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth methods available")
	}
	return methods, nil
}
