// Package pool manages the Firecracker VM lifecycle on a host.
package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// VMState represents the lifecycle state of a single Firecracker VM slot.
//
// State machine:
//
//	absent → provisioned → ready ⇄ draining → idle → deprovisioned → absent
type VMState string

const (
	// VMStateAbsent means the slot has not been provisioned yet.
	VMStateAbsent VMState = "absent"
	// VMStateProvisioned means Firecracker has been started for this slot.
	VMStateProvisioned VMState = "provisioned"
	// VMStateReady means the VM is accepting agent sessions.
	VMStateReady VMState = "ready"
	// VMStateDraining means the VM is finishing its current session before shutting down.
	VMStateDraining VMState = "draining"
	// VMStateIdle means the VM completed its session and is waiting for reassignment or shutdown.
	VMStateIdle VMState = "idle"
	// VMStateDeprovisioned means Firecracker has stopped and resources are released.
	VMStateDeprovisioned VMState = "deprovisioned"
)

// DrainMode controls how the VM handles in-flight sessions when draining.
type DrainMode string

const (
	// DrainGraceful waits for the current session to complete naturally.
	DrainGraceful DrainMode = "graceful"
	// DrainForce kills the session immediately.
	DrainForce DrainMode = "force"
	// DrainSpot is an aggressive drain with a 120-second grace period (spot-warning).
	DrainSpot DrainMode = "spot"
)

// VM represents one Firecracker microVM slot on a host.
type VM struct {
	mu        sync.Mutex
	slotIndex int
	state     VMState
	sessionID string    // active session, if any
	startedAt time.Time // when the current session started
	pid       int       // Firecracker process PID (0 = not running)
}

// NewVM creates a VM in the absent state.
func NewVM(slotIndex int) *VM {
	return &VM{slotIndex: slotIndex, state: VMStateAbsent}
}

// SlotIndex returns the zero-based slot index on the host.
func (v *VM) SlotIndex() int { return v.slotIndex }

// State returns the current state.
func (v *VM) State() VMState {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state
}

// SessionID returns the active session ID or empty string.
func (v *VM) SessionID() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sessionID
}

// Provision moves the VM from absent to provisioned.
func (v *VM) Provision(pid int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state != VMStateAbsent && v.state != VMStateDeprovisioned {
		return fmt.Errorf("vm slot %d: cannot provision from state %s", v.slotIndex, v.state)
	}
	v.state = VMStateProvisioned
	v.pid = pid
	return nil
}

// MarkReady moves the VM from provisioned to ready.
func (v *VM) MarkReady() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state != VMStateProvisioned {
		return fmt.Errorf("vm slot %d: cannot mark ready from state %s", v.slotIndex, v.state)
	}
	v.state = VMStateReady
	return nil
}

// Assign moves the VM from ready to draining with an active session.
// It does NOT change to draining itself — the caller is responsible for
// eventually calling Drain once the session is complete or interrupted.
func (v *VM) Assign(sessionID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state != VMStateReady {
		return fmt.Errorf("vm slot %d: cannot assign session in state %s", v.slotIndex, v.state)
	}
	v.sessionID = sessionID
	v.startedAt = time.Now()
	return nil
}

// Drain transitions the VM to the draining state using the given mode.
// It returns a channel that closes when draining is complete (the VM moves to idle).
func (v *VM) Drain(ctx context.Context, mode DrainMode) (<-chan struct{}, error) {
	v.mu.Lock()
	if v.state != VMStateReady && v.state != VMStateDraining {
		s := v.state
		v.mu.Unlock()
		return nil, fmt.Errorf("vm slot %d: cannot drain from state %s", v.slotIndex, s)
	}
	v.state = VMStateDraining
	v.mu.Unlock()

	done := make(chan struct{})

	var deadline time.Duration
	switch mode {
	case DrainSpot:
		deadline = 120 * time.Second
	case DrainForce:
		deadline = 0
	default: // DrainGraceful
		deadline = 0 // waits indefinitely for session completion signal
	}

	go func() {
		defer close(done)
		if deadline > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(deadline):
			}
		}
		v.mu.Lock()
		v.state = VMStateIdle
		v.sessionID = ""
		v.mu.Unlock()
	}()

	return done, nil
}

// fcConfig is the per-VM Firecracker JSON configuration written to disk.
type fcConfig struct {
	BootSource    fcBootSource `json:"boot-source"`
	Drives        []fcDrive   `json:"drives"`
	MachineConfig fcMachine   `json:"machine-config"`
	Vsock         fcVsock     `json:"vsock"`
}

type fcBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}

type fcDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type fcMachine struct {
	VcpuCount  int `json:"vcpu_count"`
	MemSizeMib int `json:"mem_size_mib"`
}

type fcVsock struct {
	GuestCID int    `json:"guest_cid"`
	UDSPath  string `json:"uds_path"`
}

// Spawn launches a Firecracker process for this slot and transitions
// the VM from absent/deprovisioned → provisioned → ready.
//
// Parameters:
//   - firecrackerBin: path to the firecracker binary
//   - kernelPath:     path to the Linux kernel image
//   - rootfsPath:     path to the rootfs ext4 image (base snapshot)
//   - socketPath:     path for the Firecracker management socket
func (v *VM) Spawn(ctx context.Context, firecrackerBin, kernelPath, rootfsPath, socketPath string) error {
	// Write per-slot Firecracker config JSON.
	configPath := fmt.Sprintf("/tmp/fc-config-%d.json", v.slotIndex)
	cfg := fcConfig{
		BootSource: fcBootSource{
			KernelImagePath: kernelPath,
			BootArgs:        "console=ttyS0 reboot=k panic=1 pci=off",
		},
		Drives: []fcDrive{{
			DriveID:      "rootfs",
			PathOnHost:   rootfsPath,
			IsRootDevice: true,
			IsReadOnly:   false,
		}},
		MachineConfig: fcMachine{VcpuCount: 2, MemSizeMib: 256},
		Vsock: fcVsock{
			GuestCID: v.slotIndex + 3,
			UDSPath:  socketPath + ".vsock",
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("vm slot %d: marshal firecracker config: %w", v.slotIndex, err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("vm slot %d: write firecracker config: %w", v.slotIndex, err)
	}

	// Start the Firecracker process.
	cmd := exec.CommandContext(ctx, firecrackerBin,
		"--no-api",
		"--config-file", configPath,
		"--api-sock", socketPath,
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("vm slot %d: start firecracker: %w", v.slotIndex, err)
	}
	pid := cmd.Process.Pid

	// Watch for process exit in a goroutine.
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
	}()

	// Health-check: poll for the socket file to appear (up to 2 seconds).
	const pollInterval = 100 * time.Millisecond
	const socketTimeout = 2 * time.Second
	deadline := time.Now().Add(socketTimeout)

	for {
		if _, statErr := os.Stat(socketPath); statErr == nil {
			break // socket appeared — VM is accepting connections
		}

		// Check if the process exited with an error before the socket appeared.
		select {
		case exitErr := <-cmdDone:
			if exitErr != nil {
				return fmt.Errorf("vm slot %d: firecracker exited before ready: %w", v.slotIndex, exitErr)
			}
			// Exited 0 without creating socket — not an error by itself; keep polling.
		default:
		}

		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return fmt.Errorf("vm slot %d: timed out waiting for firecracker socket %s", v.slotIndex, socketPath)
		}

		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	// Socket appeared — record the PID and mark the slot ready.
	if err := v.Provision(pid); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("vm slot %d: provision after spawn: %w", v.slotIndex, err)
	}
	if err := v.MarkReady(); err != nil {
		return fmt.Errorf("vm slot %d: mark ready after spawn: %w", v.slotIndex, err)
	}
	return nil
}

// Deprovision moves the VM from idle to deprovisioned.
func (v *VM) Deprovision() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state != VMStateIdle {
		return fmt.Errorf("vm slot %d: cannot deprovision from state %s", v.slotIndex, v.state)
	}
	v.state = VMStateDeprovisioned
	v.pid = 0
	return nil
}
