//go:build !integration

package pool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeBinary returns the path to a shell script binary in tempDir.
// If exitCode == 0, the script creates the socket file passed via --api-sock
// and exits 0. Otherwise it exits 1 immediately.
func fakeBinary(t *testing.T, tempDir string, exitCode int) string {
	t.Helper()
	var script string
	if exitCode == 0 {
		// Parse --api-sock from argv, create the socket file, then exit 0.
		script = "#!/bin/sh\n" +
			"sockpath=''\n" +
			"while [ $# -gt 0 ]; do\n" +
			"  if [ \"$1\" = '--api-sock' ]; then sockpath=\"$2\"; fi\n" +
			"  shift\n" +
			"done\n" +
			"[ -n \"$sockpath\" ] && touch \"$sockpath\"\n" +
			"exit 0\n"
	} else {
		script = "#!/bin/sh\nexit 1\n"
	}

	binPath := filepath.Join(tempDir, "fake-firecracker")
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return binPath
}

func TestVMSpawnSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	tempDir := t.TempDir()
	socketPath := filepath.Join(tempDir, "fc.sock")
	kernelPath := filepath.Join(tempDir, "vmlinux")
	rootfsPath := filepath.Join(tempDir, "rootfs.ext4")

	// Create placeholder kernel and rootfs files so the config JSON is valid.
	for _, p := range []string{kernelPath, rootfsPath} {
		if err := os.WriteFile(p, []byte("placeholder"), 0600); err != nil {
			t.Fatalf("create placeholder %s: %v", p, err)
		}
	}

	binPath := fakeBinary(t, tempDir, 0)

	vm := NewVM(2)
	ctx := context.Background()

	if err := vm.Spawn(ctx, binPath, kernelPath, rootfsPath, socketPath); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// VM must have reached the ready state.
	if vm.State() != VMStateReady {
		t.Fatalf("expected ready state after Spawn, got %s", vm.State())
	}

	// Verify the Firecracker config JSON was written with correct fields.
	configPath := "/tmp/fc-config-2.json"
	defer func() { _ = os.Remove(configPath) }()

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config JSON: %v", err)
	}

	var cfg fcConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config JSON: %v", err)
	}

	if cfg.BootSource.KernelImagePath != kernelPath {
		t.Errorf("kernel path: got %q, want %q", cfg.BootSource.KernelImagePath, kernelPath)
	}
	if cfg.BootSource.BootArgs == "" {
		t.Error("boot args must not be empty")
	}
	if len(cfg.Drives) != 1 || cfg.Drives[0].PathOnHost != rootfsPath {
		t.Errorf("rootfs path: got %q, want %q",
			func() string {
				if len(cfg.Drives) > 0 {
					return cfg.Drives[0].PathOnHost
				}
				return ""
			}(),
			rootfsPath)
	}
	if !cfg.Drives[0].IsRootDevice {
		t.Error("rootfs drive must be root device")
	}
	if cfg.Drives[0].IsReadOnly {
		t.Error("rootfs drive must not be read-only")
	}
	// guest CID = slotIndex + 3 = 2 + 3 = 5
	if cfg.Vsock.GuestCID != 5 {
		t.Errorf("guest CID: got %d, want 5", cfg.Vsock.GuestCID)
	}
	if cfg.Vsock.UDSPath != socketPath+".vsock" {
		t.Errorf("vsock UDS path: got %q, want %q", cfg.Vsock.UDSPath, socketPath+".vsock")
	}
	if cfg.MachineConfig.VcpuCount != 2 {
		t.Errorf("vcpu count: got %d, want 2", cfg.MachineConfig.VcpuCount)
	}
	if cfg.MachineConfig.MemSizeMib != 256 {
		t.Errorf("mem size: got %d, want 256", cfg.MachineConfig.MemSizeMib)
	}
}

func TestVMSpawnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	tempDir := t.TempDir()
	socketPath := filepath.Join(tempDir, "fc.sock")
	kernelPath := filepath.Join(tempDir, "vmlinux")
	rootfsPath := filepath.Join(tempDir, "rootfs.ext4")

	for _, p := range []string{kernelPath, rootfsPath} {
		if err := os.WriteFile(p, []byte("placeholder"), 0600); err != nil {
			t.Fatalf("create placeholder %s: %v", p, err)
		}
	}

	// Use a binary that exits 1 immediately (simulates firecracker startup failure).
	binPath := fakeBinary(t, tempDir, 1)

	vm := NewVM(0)
	ctx := context.Background()

	err := vm.Spawn(ctx, binPath, kernelPath, rootfsPath, socketPath)
	if err == nil {
		t.Fatal("expected Spawn to return an error for a failing binary, got nil")
	}

	// VM must remain in absent state — Provision must not have been called.
	if vm.State() != VMStateAbsent {
		t.Fatalf("expected absent state after failed Spawn, got %s", vm.State())
	}
}

func TestVMSpawnSocketTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	tempDir := t.TempDir()
	socketPath := filepath.Join(tempDir, "fc.sock")
	kernelPath := filepath.Join(tempDir, "vmlinux")
	rootfsPath := filepath.Join(tempDir, "rootfs.ext4")

	for _, p := range []string{kernelPath, rootfsPath} {
		if err := os.WriteFile(p, []byte("placeholder"), 0600); err != nil {
			t.Fatalf("create placeholder %s: %v", p, err)
		}
	}

	// Binary that exits 0 but never creates the socket (simulates a VM that starts
	// but does not become responsive within the 2-second health-check window).
	binPath := filepath.Join(tempDir, "fake-firecracker-silent")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write silent binary: %v", err)
	}

	vm := NewVM(1)
	ctx := context.Background()

	err := vm.Spawn(ctx, binPath, kernelPath, rootfsPath, socketPath)
	if err == nil {
		t.Fatal("expected Spawn to return an error when socket never appears, got nil")
	}

	// VM must remain in absent state.
	if vm.State() != VMStateAbsent {
		t.Fatalf("expected absent state after socket timeout, got %s", vm.State())
	}
}
