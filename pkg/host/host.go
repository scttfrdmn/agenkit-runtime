// Package host defines the Host interface and its implementations.
//
// A Host represents a physical or virtual machine that runs a pool of
// Firecracker microVMs. Two implementations are provided:
//   - LocalHost: SSH to an existing machine
//   - EC2Host: Launch an EC2 spot instance and bootstrap it
package host

import (
	"context"
)

// ProvisionConfig carries parameters used when bootstrapping a new host.
type ProvisionConfig struct {
	// KernelPath is the path to the guest vmlinux on the host.
	KernelPath string
	// SnapshotStore is the URI of the snapshot store (local path or S3 URL).
	SnapshotStore string
	// AgentRuntimeVersion is the version of agenkit-runtime to install on the host.
	AgentRuntimeVersion string
	// ExtraEnv holds additional environment variables to export on the host.
	ExtraEnv map[string]string
}

// Host represents a machine that manages a pool of Firecracker microVMs.
type Host interface {
	// Provision bootstraps the host (installs dependencies, configures Firecracker).
	Provision(ctx context.Context, cfg ProvisionConfig) error

	// Address returns the network address used to reach the host (hostname or IP).
	Address() string

	// PoolSize returns the number of VM slots configured for this host.
	PoolSize() int

	// Type returns the host type: "local" or "ec2".
	Type() string

	// Terminate shuts down the host. For EC2 hosts this terminates the instance.
	// For local hosts this is a no-op.
	Terminate(ctx context.Context) error
}
