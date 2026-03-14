// Package api provides the Unix socket management API for the agenkit-runtime daemon.
//
// The daemon listens on a Unix socket at SocketPath and accepts JSON
// newline-delimited request/response pairs. CLI subcommands connect to this
// socket to perform host, cluster, and snapshot management operations.
package api

// SocketPath is the default path for the daemon's Unix socket.
const SocketPath = "/var/run/agenkit/runtime.sock"

// Request is a management command sent from a CLI subcommand to the daemon.
type Request struct {
	// Command is the dot-namespaced command name, e.g. "host.add".
	Command string `json:"command"`
	// Args carries command-specific key/value arguments.
	Args map[string]string `json:"args,omitempty"`
}

// Response is the daemon's reply to a Request.
type Response struct {
	// OK is true if the command succeeded.
	OK bool `json:"ok"`
	// Data holds the command-specific response payload on success.
	Data interface{} `json:"data,omitempty"`
	// Error is a human-readable error message on failure.
	Error string `json:"error,omitempty"`
}
