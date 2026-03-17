# Claude Code Guidelines for agenkit-runtime

## What This Repo Is

`agenkit-runtime` is the **host-side daemon and CLI** that manages pools of
[Firecracker](https://firecracker-microvm.github.io/) microVMs for the
[agenkit](https://github.com/scttfrdmn/agenkit) ecosystem.

It is a standalone Go module (`github.com/scttfrdmn/agenkit-runtime`). It does **not**
import agenkit-go as a Go dependency — the integration points are interface contracts
(described below) that must be manually kept in sync.

### Responsibilities

| Package | Responsibility |
|---------|---------------|
| `cmd/` | Cobra CLI: `serve`, `recover`, `cluster`, `host`, `snapshot` subcommands |
| `pkg/config/` | YAML cluster config + JSON state persistence |
| `pkg/host/` | `Host` interface; `LocalHost` (SSH) and `EC2Host` (spot) implementations |
| `pkg/provision/` | SSH bootstrap: installs Firecracker + agenkit-runtime via apt + systemd |
| `pkg/pool/` | Fixed-size VM slot array; VM state machine (`absent→provisioned→ready⇄draining→idle→deprovisioned`) |
| `pkg/migration/` | Spot interruption monitor (IMDS); `Migrator` orchestrates checkpoint signals; `MigrationManifest` data structures |
| `pkg/snapshot/` | `SnapshotStore` interface; `LocalStore`; `Manager` lifecycle (build/push/pull) |
| `pkg/vsock/` | Host↔guest vsock signalling protocol (JSON newline-delimited); `Bus` connection wrapper |

---

## Build & Test

```bash
# Build everything
go build ./...

# Run all tests (none yet — see issue #11)
go test ./...

# Format
gofmt -w .

# Lint
golangci-lint run ./...
```

No CI/CD — local validation only.

---

## Architecture

### Data Flow: Spot Eviction

```
EC2 IMDS (polling every 5s)
  └─ SpotMonitor.OnInterruption(deadline)          [pkg/migration/spot.go]
       └─ Migrator.MigrateAll(ctx, sessions, deadline)  [pkg/migration/migrator.go]
            ├─ For each active VM slot:
            │    vsock.Bus.RequestCheckpoint(...)        [pkg/vsock/bus.go]
            │         → HostSignal{Type:"checkpoint_now"} sent to guest
            │         ← GuestAck{CheckpointID:"..."} received from guest
            └─ MigrationManifest written to snapshot_store/manifests/
                 └─ After host recovery: `agenkit-runtime recover`
                      └─ DurableAgent.ResumeMigrated(ctx, MigrationContext)
                           (bridge to agenkit-go checkpointing package)
```

### Data Flow: Normal Session Assignment

```
CLI / external caller
  └─ Pool.Acquire(sessionID)                       [pkg/pool/pool.go]
       └─ VM.Assign(sessionID)                     [pkg/pool/vm.go]
            └─ Firecracker process already running (pid stored in VM)
```

### IPC: CLI ↔ Daemon

The `serve` daemon exposes a Unix socket at `/var/run/agenkit/runtime.sock`.
CLI subcommands (`host add`, `cluster status`, etc.) connect to this socket
via `pkg/api`. Implemented in v0.1.0.

---

## Integration with agenkit-go

`agenkit-runtime` does not import `agenkit-go` directly. The vsock bridge is
the integration point: when `recover` runs, it sends `resume_migrated` signals
over vsock to running VM slots. The guest's agenkit-go agent handles the signal
and calls `DurableAgent.ResumeMigrated` internally.

| vsock signal | Used where | Purpose |
|-------------|-----------|---------|
| `resume_migrated` | `cmd/internal/recover.go` → `pkg/vsock/bus.RequestResume` | Ask a fresh guest to restore a checkpointed session |
| `checkpoint_now` | `pkg/migration/migrator.go` → `pkg/vsock/bus.RequestCheckpoint` | Ask a running guest to create a checkpoint |

---

## Current Implementation State (v0.5.0)

### Implemented ✅

| Feature | Package | Notes |
|---------|---------|-------|
| Unix socket management API | `pkg/api/` | Serve + CLI IPC |
| Host/cluster/snapshot commands | `cmd/internal/` | Wired end-to-end |
| Session recovery (manifest) | `cmd/internal/recover.go` | Reads manifests + vsock bridge |
| Manifest persistence | `pkg/migration/migrator.go` | Atomic write on eviction |
| Firecracker snapshot UDS API | `pkg/snapshot/manager.go` | Pause → snapshot → resume |
| S3 snapshot store | `pkg/snapshot/s3store.go` | Multipart upload/download |
| VM pool wiring | `cmd/internal/serve.go` | Pre-warm, drain on SIGTERM |
| Unit + integration tests | `*_test.go` | 17 tests across 4 packages |
| Structured logging (slog) | `cmd/internal/serve.go` | JSON handler + log level flag |
| Prometheus metrics | `pkg/metrics/` | pool slots, migration outcomes |
| Firecracker process spawning | `pkg/pool/vm.go` (`Spawn`) | Config JSON + socket health-check |
| ResumeMigrated vsock bridge | `pkg/vsock/bus.go`, `recover.go` | `resume_migrated` signal + ack |

### Remaining Operational Prerequisites (not code stubs)

These require infrastructure setup, not code changes:

1. **Base snapshot build**: A pre-built kernel + rootfs must exist at the paths
   configured in `cluster.yaml` (`kernel_path`). Production build instructions
   are out of scope for this repo.

2. **TAP device / networking**: Firecracker requires TAP devices for guest
   networking. Setup (`ip tuntap add`, `bridge` config) is host provisioning
   work done by `pkg/provision/` during `cluster provision`.

3. **Real vsock (Linux only)**: `pkg/vsock/bus.go` currently uses TCP as a
   fallback. On production Linux hosts with `vhost-vsock` loaded, the dialer
   should use `AF_VSOCK`. A follow-on issue tracks this.

4. **EC2 nested-virt verification**: The `pkg/host/ec2.go` uses the `metal`
   instance type pattern for nested virtualization. Verify the correct AWS API
   field for the Feb 2026 SDK version before production use.

---

## Key Conventions (from parent project)

### Go Idioms (must follow)

```go
// Error handling — always check or explicitly discard
defer func() { _ = file.Close() }()
if _, err := w.Write(data); err != nil { return fmt.Errorf("write: %w", err) }

// time.Duration — never pass raw floats to Printf
log.Printf("timeout=%.1fs", d.Seconds())  // NOT: log.Printf("%.1f", d)

// Switch over if-else chains
switch state {
case VMStateReady:   ...
case VMStateDraining: ...
}

// Error messages start lowercase
return fmt.Errorf("failed to provision host %s: %w", addr, err)

// Build tags
//go:build linux   (NOT old // +build linux)

// Network deadline errors must be checked
if err := conn.SetReadDeadline(t); err != nil { return err }
```

### Atomic File Writes

`config.SaveState` uses atomic write (write to temp + rename). Follow the same pattern
for manifest files and any other persistent state:

```go
tmp := path + ".tmp"
if err := os.WriteFile(tmp, data, 0600); err != nil { return err }
return os.Rename(tmp, path)
```

### Context Propagation

All blocking operations accept `context.Context` as first argument. Respect cancellation:

```go
select {
case <-done:
case <-ctx.Done():
    return ctx.Err()
}
```

### Config & State File Locations

| File | Purpose |
|------|---------|
| `/etc/agenkit/cluster.yaml` | ClusterConfig (read-only at runtime) |
| `/etc/agenkit/cluster-state.json` | ClusterState (read-write by daemon) |
| `/var/run/agenkit/runtime.sock` | Unix socket for CLI↔daemon IPC (to implement) |
| `{snapshot_store}/manifests/{migration_id}.json` | MigrationManifest per eviction event |

---

## GitHub Issues

Work is tracked in GitHub Issues on this repo. Milestones map to releases:

| Milestone | Focus |
|-----------|-------|
| v0.1.0 | Unix socket management API; host/cluster commands wired end-to-end |
| v0.2.0 | Session recovery: manifest persistence + ResumeMigrated integration |
| v0.3.0 | Firecracker integration: real snapshot build; S3 store; pool in daemon |
| v0.4.0 | Tests & observability: unit tests, structured logging, Prometheus metrics |

Start with issues in the v0.1.0 milestone.

---

## Quick Orientation

```bash
# Read the serve daemon entry point
cat cmd/internal/serve.go

# See the VM state machine
cat pkg/pool/vm.go

# Understand the migration flow
cat pkg/migration/migrator.go
cat pkg/migration/manifest.go

# See what snapshot build does (stub)
cat pkg/snapshot/manager.go

# Example cluster config
cat deploy/cluster.example.yaml
```

---

**Last Updated:** March 2026 (initial scaffold complete; stubs remain — see Issues)
