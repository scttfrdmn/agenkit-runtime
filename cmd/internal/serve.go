package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/scttfrdmn/agenkit-runtime/cmd/internal/handlers"
	"github.com/scttfrdmn/agenkit-runtime/pkg/api"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/metrics"
	"github.com/scttfrdmn/agenkit-runtime/pkg/migration"
	"github.com/scttfrdmn/agenkit-runtime/pkg/pool"
	"github.com/scttfrdmn/agenkit-runtime/pkg/snapshot"
)

func serveCmd() *cobra.Command {
	var logLevel string
	var metricsAddr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the agenkit-runtime host daemon",
		Long: `serve starts the long-running daemon that:
  - Maintains a pool of Firecracker microVMs per configured host
  - Reconciles cluster state on startup
  - Monitors for spot interruption notices (no-op on non-EC2)
  - Accepts vsock connections from guest agents
  - Provides a Unix-socket management API for CLI sub-commands`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), logLevel, metricsAddr)
		},
	}

	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	cmd.Flags().StringVar(&metricsAddr, "metrics-addr", ":9090", "Address for the Prometheus /metrics endpoint")
	return cmd
}

func runServe(ctx context.Context, logLevelStr string, metricsAddr string) error {
	// Configure structured JSON logging.
	var level slog.Level
	switch logLevelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	// Register and start Prometheus metrics endpoint.
	metrics.Register()
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(metricsAddr, mux); err != nil {
			slog.Error("metrics server stopped", "err", err)
		}
	}()

	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		return err
	}

	state, err := config.LoadState(config.DefaultStatePath)
	if err != nil {
		slog.Warn("failed to load cluster state, starting fresh", "err", err)
		state = &config.ClusterState{Hosts: make(map[string]*config.HostState)}
	}

	slog.Info("serve starting", "hosts", len(cfg.Hosts))
	slog.Info("cluster state loaded", "known_hosts", len(state.Hosts))

	// Build per-host pools from state and pre-warm slots that were ready
	// at last shutdown (as recorded in VMStates).
	pools := make(map[string]*pool.Pool, len(state.Hosts))
	for addr, hs := range state.Hosts {
		p := pool.NewPool(hs.PoolSize)
		pools[addr] = p

		for i, stateStr := range hs.VMStates {
			if i >= p.Size() {
				break
			}
			if pool.VMState(stateStr) != pool.VMStateReady {
				continue
			}
			if provErr := p.VM(i).Provision(0); provErr != nil {
				slog.Warn("pre-warm slot provision failed", "slot", i, "host", addr, "err", provErr)
				continue
			}
			if markErr := p.VM(i).MarkReady(); markErr != nil {
				slog.Warn("pre-warm slot mark-ready failed", "slot", i, "host", addr, "err", markErr)
				continue
			}
			slog.Info("pre-warmed pool slot", "slot", i, "host", addr)
		}
	}

	// Build snapshot manager using NewStoreFromURL so s3:// and local paths
	// are both supported without changes to the config schema.
	snapshotDir := cfg.SnapshotStore
	if snapshotDir == "" {
		snapshotDir = "/var/lib/agenkit/snapshots"
	}

	// Create a cancellable context for the monitor and API server.
	monCtx, monCancel := context.WithCancel(ctx)
	defer monCancel()

	var snapshotMgr *snapshot.Manager
	if store, storeErr := snapshot.NewStoreFromURL(monCtx, snapshotDir); storeErr != nil {
		slog.Warn("failed to create snapshot store", "path", snapshotDir, "err", storeErr)
	} else {
		snapshotMgr = snapshot.NewManager(store, os.TempDir())
	}

	// Background goroutine: update PoolVMSlots gauge every 15 seconds.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monCtx.Done():
				return
			case <-ticker.C:
				for addr, p := range pools {
					for state, count := range p.Stats() {
						metrics.PoolVMSlots.WithLabelValues(addr, string(state)).Set(float64(count))
					}
				}
			}
		}
	}()

	// Start the spot monitor (no-op on non-EC2).
	manifestDir := snapshotDir + "/manifests"
	spotMon := migration.NewSpotMonitor(func(deadline time.Time) {
		slog.Warn("spot interruption detected, initiating migration", "deadline", deadline)

		// Collect active sessions and vsock addresses from all pools.
		activeSessions := make(map[int]string)
		vmAddrs := make(map[int]string)
		for _, p := range pools {
			for slotIdx, sessionID := range p.ActiveSessions() {
				activeSessions[slotIdx] = sessionID
				vmAddrs[slotIdx] = fmt.Sprintf("vsock:%d", slotIdx+3)
			}
		}
		if len(activeSessions) == 0 {
			slog.Info("spot interruption: no active sessions to migrate")
			return
		}

		// Generate a random migration ID (16 bytes of hex — no external uuid dep).
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			slog.Error("migration: failed to generate migration ID", "err", err)
			return
		}
		migrationID := hex.EncodeToString(raw[:])

		// Resolve local host address from config.
		hostAddr := "localhost"
		if len(cfg.Hosts) > 0 && cfg.Hosts[0].Addr != "" {
			hostAddr = cfg.Hosts[0].Addr
		}

		migrator := &migration.Migrator{
			HostAddr:    hostAddr,
			VMAddrs:     vmAddrs,
			MigrationID: migrationID,
			Reason:      "spot_warning",
			ManifestDir: manifestDir,
		}

		go func() {
			manifest, err := migrator.MigrateAll(monCtx, activeSessions, deadline)
			if err != nil {
				slog.Error("migration MigrateAll failed", "migration_id", migrationID, "err", err)
				return
			}
			pending := 0
			for _, s := range manifest.Sessions {
				switch s.Status {
				case "pending":
					pending++
					metrics.MigrationSessionsTotal.WithLabelValues("pending").Inc()
				case "failed":
					metrics.MigrationSessionsTotal.WithLabelValues("failed").Inc()
				}
			}
			slog.Info("migration complete", "migration_id", migrationID, "pending", pending, "total", len(manifest.Sessions))
		}()
	})

	go spotMon.Run(monCtx)

	// Start the Unix socket API server.
	srv := api.NewServer(api.SocketPath)
	srv.Register("host.add", handlers.HostAdd(cfg, state, config.DefaultStatePath))
	srv.Register("host.list", handlers.HostList(state))
	srv.Register("host.remove", handlers.HostRemove(state, pools, config.DefaultStatePath))
	srv.Register("host.drain", handlers.HostDrain(pools))
	srv.Register("host.resume", handlers.HostResume(state, config.DefaultStatePath))
	srv.Register("cluster.status", handlers.ClusterStatus(state, pools))
	srv.Register("cluster.provision", handlers.ClusterProvision(cfg, state, config.DefaultStatePath))
	srv.Register("cluster.teardown", handlers.ClusterTeardown(state, pools, config.DefaultStatePath))
	if snapshotMgr != nil {
		srv.Register("snapshot.build", handlers.SnapshotBuild(snapshotMgr))
		srv.Register("snapshot.push", handlers.SnapshotPush(snapshotMgr))
		srv.Register("snapshot.pull", handlers.SnapshotPull(snapshotMgr))
	}

	go func() {
		if err := srv.Serve(monCtx); err != nil {
			slog.Warn("api server error", "err", err)
		}
	}()

	// Wait for termination signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	// Persist VM states before shutdown so the next start can pre-warm correctly.
	for addr, p := range pools {
		if hs, ok := state.Hosts[addr]; ok {
			vmStates := make([]string, p.Size())
			for i := range vmStates {
				vmStates[i] = string(p.VM(i).State())
			}
			hs.VMStates = vmStates
		}
	}

	// Drain all pools with a 30-second graceful timeout.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	for addr, p := range pools {
		slog.Info("draining pool", "host", addr, "active_sessions", len(p.ActiveSessions()))
		p.Drain(drainCtx, pool.DrainGraceful)
	}

	// Persist final state.
	if err := config.SaveState(config.DefaultStatePath, state); err != nil {
		slog.Warn("failed to save cluster state on shutdown", "err", err)
	}
	return nil
}
