package host

import (
	"context"
	"fmt"

	"github.com/scttfrdmn/agenkit-runtime/pkg/provision"
)

// LocalHost connects to an existing machine via SSH and manages Firecracker VMs on it.
type LocalHost struct {
	addr     string
	user     string
	poolSize int
}

// NewLocalHost creates a LocalHost that will SSH to addr as user.
func NewLocalHost(addr, user string, poolSize int) *LocalHost {
	return &LocalHost{addr: addr, user: user, poolSize: poolSize}
}

// Provision installs and configures Firecracker on the host via SSH.
func (h *LocalHost) Provision(ctx context.Context, cfg ProvisionConfig) error {
	conn, err := provision.Dial(ctx, h.user, h.addr)
	if err != nil {
		return fmt.Errorf("provision: ssh dial %s: %w", h.addr, err)
	}
	defer conn.Close()
	return provision.Bootstrap(ctx, conn, provision.BootstrapConfig{
		KernelPath:          cfg.KernelPath,
		SnapshotStore:       cfg.SnapshotStore,
		AgentRuntimeVersion: cfg.AgentRuntimeVersion,
		ExtraEnv:            cfg.ExtraEnv,
	})
}

// Address returns the hostname or IP of the machine.
func (h *LocalHost) Address() string { return h.addr }

// PoolSize returns the number of VM slots configured.
func (h *LocalHost) PoolSize() int { return h.poolSize }

// Type returns "local".
func (h *LocalHost) Type() string { return "local" }

// Terminate is a no-op for local hosts.
func (h *LocalHost) Terminate(_ context.Context) error { return nil }
