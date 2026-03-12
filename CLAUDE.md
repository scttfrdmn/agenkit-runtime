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

### IPC: CLI ↔ Daemon (NOT YET IMPLEMENTED)

The `serve` daemon must expose a Unix socket at `/var/run/agenkit/runtime.sock`.
CLI subcommands (`host add`, `cluster status`, etc.) connect to this socket.
See GitHub issue #1 for the full spec.

---

## Integration with agenkit-go

These types from `github.com/scttfrdmn/agenkit-go/checkpointing` are used by
the `recover` command. They are **not imported** — they are called through an
interface that must be kept in sync manually.

| agenkit-go type | Used where | Purpose |
|-----------------|-----------|---------|
| `DurableAgent.ResumeMigrated(ctx, MigrationContext)` | `cmd/internal/recover.go` | Resume an agent session from a checkpoint after spot eviction |
| `MigrationContext` | `pkg/migration/manifest.go` | Carries `SourceHost`, `CheckpointID`, `SessionID`, `MigrationID` to the agent |
| `SharedCheckpointStorage` | `pkg/snapshot/store.go` | Marker interface — `LocalStore` and future `S3Store` must satisfy it |

The bridge: when `recover` runs, for each `SessionMigration` with `Status=="pending"`,
it constructs a `MigrationContext` from the manifest and calls into the agenkit-go
library (via subprocess or RPC, TBD in issue #5).

---

## Stubs Inventory

Every unimplemented piece with exact file + line:

### Critical Path (must implement first)

| # | File | Line(s) | Description |
|---|------|---------|-------------|
| 1 | `cmd/internal/serve.go` | 50 | `OnInterruption` callback: TODO comment, no MigrateAll call |
| 2 | `cmd/internal/serve.go` | — | No Unix socket IPC listener (entire feature missing) |
| 3 | `pkg/snapshot/manager.go` | 26–42 | `Build()` writes JSON marker file only; no Firecracker UDS call |

### Host & Cluster Commands (all stubs)

| # | File | Line(s) | Description |
|---|------|---------|-------------|
| 4 | `cmd/internal/host.go` | 32–97 | All 5 host subcommands print messages only |
| 5 | `cmd/internal/cluster.go` | 27–52 | `provision` and `teardown` print messages only |
| 6 | `cmd/internal/snapshot.go` | 35–57 | `push` and `pull` print messages only |

### Session Recovery

| # | File | Line(s) | Description |
|---|------|---------|-------------|
| 7 | `cmd/internal/recover.go` | 38–44 | Prints "Recovery complete" — no manifest reading, no ResumeMigrated call |
| 8 | `pkg/migration/migrator.go` | — | `MigrateAll` never writes manifest to disk; manifests lost on crash |

### Missing Entirely

| # | What | Description |
|---|------|-------------|
| 9 | `pkg/api/` | Unix socket management API (new package needed) |
| 10 | `pkg/snapshot/s3store.go` | S3-backed SnapshotStore (new file needed) |
| 11 | `*_test.go` | No tests anywhere in repo |
| 12 | Structured logging + metrics | `serve.go` uses `log.Printf` throughout |

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
