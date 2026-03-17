# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-03-17

### Added

#### Firecracker Process Spawning
- **`pkg/pool/vm.go`**: `Spawn(ctx, firecrackerBin, kernelPath, rootfsPath, socketPath)`
  launches a Firecracker process, writes per-slot config JSON to
  `/tmp/fc-config-{slot}.json`, polls for socket readiness (up to 2s),
  then calls `Provision(pid)` → `MarkReady()`
- Config JSON includes: `boot-source`, `drives` (rootfs), `machine-config`
  (2 vCPU / 256 MiB), and `vsock` (guest CID = slot+3)
- **`pkg/pool/vm_spawn_test.go`**: 3 new tests — success path (config fields +
  state transitions), failure path (binary exits 1 → VM stays absent),
  socket timeout (binary exits 0 but no socket → VM stays absent)

#### ResumeMigrated Bridge
- **`pkg/vsock/protocol.go`**: Added `SignalResumeMigrated` signal type; added
  `CheckpointID` field to `HostSignal` (used for resume signals)
- **`pkg/vsock/bus.go`**: Added `RequestResume(ctx, checkpointID, migrationID,
  deadlineSec)` — sends `resume_migrated` signal and waits for guest ack
- **`cmd/internal/recover.go`**: Replaced "unrecoverable" stub with a real
  vsock bridge — pending sessions now trigger a `resume_migrated` signal to
  an acquired pool slot; manifest updated to `resumed` or `failed` (with
  error detail); no sessions are silently discarded

#### Integration Test
- **`cmd/integration_test.go`**: Local cluster lifecycle test (provision → assign →
  drain → deprovision) using `mockSpawn` (no real Firecracker binary required);
  run with `go test -tags integration ./cmd/...`

## [0.4.0] - 2026-03-15

### Added

#### Unit Tests — Issue #11
- **`pkg/pool/pool_test.go`**: 7 tests covering VM state machine transitions,
  invalid transitions, `Acquire`/release cycle, `Stats()`, `ActiveSessions()`,
  `Drain()`, and `Available()` count
- **`pkg/migration/migrator_test.go`**: 4 tests using TCP listeners as fake vsock
  responders — success path, guest error (OOM), manifest persistence, and
  context cancellation; all run on macOS and Linux
- **`pkg/vsock/bus_test.go`**: 3 tests — `RequestCheckpoint` round-trip,
  `HostSignal` marshal/unmarshal fidelity, and timeout when guest hangs
- **`pkg/snapshot/store_test.go`**: 3 tests — `NewStoreFromURL` with local path
  returns `*LocalStore`; S3 URL returns `*S3SnapshotStore` (skipped in `-short`
  mode); empty URL returns an error

#### Structured Logging — Issue #12
- **`cmd/internal/serve.go`**: Replaced all `log.Printf` calls with structured
  `slog` calls (`slog.Info`, `slog.Warn`, `slog.Error`)
- JSON log handler (`slog.NewJSONHandler`) enabled at startup for
  machine-readable structured output
- New `--log-level` flag (debug / info / warn / error, default: info)

#### Prometheus Metrics — Issue #12
- **`pkg/metrics/metrics.go`**: New package exporting three Prometheus metrics:
  `agenkit_pool_vm_slots` (gauge, per host+state),
  `agenkit_migration_sessions_total` (counter, by outcome),
  `agenkit_snapshot_ops_total` (counter, by operation+status)
- New `--metrics-addr` flag (default `:9090`); HTTP `/metrics` endpoint served
  via `promhttp.Handler`
- Background goroutine (15s ticker) updates `PoolVMSlots` gauge from pool stats
- Migration session outcomes increment `MigrationSessionsTotal` counter
- New dependency: `github.com/prometheus/client_golang v1.23.2`

## [0.3.0] - 2026-03-15

### Added

#### Firecracker Snapshot UDS API (Issue #8)
- **`pkg/snapshot/manager.go`**: Replace stub `Build()` with real Firecracker
  Management API calls over the per-VM Unix socket
  (`/run/firecracker-{pid}.sock` by default)
- Sequence: `PATCH /vm {"state":"Paused"}` → `PUT /snapshot/create` →
  `PATCH /vm {"state":"Resumed"}`; VM is always resumed even when the
  snapshot step fails
- Added `NewManagerWithSocket(store, buildDir, socketPathFn)` constructor for
  testable socket-path injection; `NewManager` unchanged (backward compatible)

#### S3 Snapshot Store (Issue #9)
- **`pkg/snapshot/s3store.go`**: New `S3SnapshotStore` implementing
  `SnapshotStore` — credentials from the standard AWS credential chain
- Automatic multipart upload for files > 5 MB via `s3manager.Uploader`
- Key layout: `{prefix}/snapshots/{name}/vm.{snap,mem}`,
  `{prefix}/manifests/{id}.json`
- Batched deletion (up to 1000 objects per S3 `DeleteObjects` call)
- **`pkg/snapshot/store.go`**: Added `NewStoreFromURL(ctx, rawURL)` dispatcher
  — `s3://bucket/prefix` creates an `S3SnapshotStore`; any other string
  creates a `LocalStore`

#### VM Pool Wiring (Issue #10)
- **`pkg/config/cluster.go`**: Added `VMStates []string` field to `HostState`
  — persists per-slot VM state at shutdown for restart reconciliation
- **`pkg/pool/pool.go`**: Added `VM(i int) *VM` accessor for indexed slot access
- **`cmd/internal/serve.go`**: Use `NewStoreFromURL` instead of `NewLocalStore`
  directly, enabling S3 snapshot stores via cluster config
- Pool pre-warming on startup: slots that were `ready` at last shutdown are
  transitioned `absent→provisioned→ready` so the daemon can accept sessions
  immediately after restart
- Graceful 30-second drain on SIGTERM/SIGINT: all pools drained with
  `DrainGraceful` before state is persisted and the process exits
- VM states persisted into `HostState.VMStates` before each drain + save

## [0.2.0] - 2026-03-15

### Added

#### Session Recovery — manifest persistence + recover command (#5, #6, #7)
- **`pkg/migration/migrator.go`**: Added `ManifestDir string` field to `Migrator`;
  `MigrateAll` now calls `writeManifest` at completion — atomic write-then-rename
  to `{ManifestDir}/{migration_id}.json` (permissions 0600)
- **`pkg/pool/pool.go`**: Added `ActiveSessions() map[int]string` — returns slot index
  → session ID for all assigned VMs; used by the spot-interruption callback
- **`cmd/internal/serve.go`**: Wired `OnInterruption` callback end-to-end:
  - Collects active sessions from all pools via `ActiveSessions()`
  - Builds vsock addresses as `vsock:{slot+3}` stubs
  - Generates 16-byte `crypto/rand` hex migration ID (no external uuid dep)
  - Constructs `Migrator` with `ManifestDir: snapshotDir+"/manifests"` and calls
    `MigrateAll` in a goroutine; logs pending session count on completion
- **`cmd/internal/recover.go`**: Full replacement of the "Recovery complete" stub:
  - Reads all `*.json` files from `{snapshot_store}/manifests/`
  - Prints session table: migration ID | session ID | checkpoint ID | status
  - Marks `pending` sessions as `unrecoverable` (ResumeMigrated bridge planned for v0.3.0)
  - Rewrites manifest atomically after status update
  - Returns exit code 1 if any sessions were unrecoverable

## [0.1.0] - 2026-03-14

### Added

- Unix socket management API (`pkg/api/`) — JSON-over-Unix-socket IPC between CLI and daemon
- `serve` daemon opens `/var/run/agenkit/runtime.sock` and dispatches typed RPC requests
- All host subcommands wired end-to-end: `host add`, `host list`, `host remove`, `host drain`, `host resume`
- Cluster commands wired: `cluster provision`, `cluster teardown`, `cluster status`
- Snapshot commands wired: `snapshot build`, `snapshot push`, `snapshot pull`
