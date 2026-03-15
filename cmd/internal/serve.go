package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/cmd/internal/handlers"
	"github.com/scttfrdmn/agenkit-runtime/pkg/api"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/migration"
	"github.com/scttfrdmn/agenkit-runtime/pkg/pool"
	"github.com/scttfrdmn/agenkit-runtime/pkg/snapshot"
)

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the agenkit-runtime host daemon",
		Long: `serve starts the long-running daemon that:
  - Maintains a pool of Firecracker microVMs per configured host
  - Reconciles cluster state on startup
  - Monitors for spot interruption notices (no-op on non-EC2)
  - Accepts vsock connections from guest agents
  - Provides a Unix-socket management API for CLI sub-commands`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context())
		},
	}
}

func runServe(ctx context.Context) error {
	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		return err
	}

	state, err := config.LoadState(config.DefaultStatePath)
	if err != nil {
		log.Printf("WARNING: failed to load cluster state: %v (starting fresh)", err)
		state = &config.ClusterState{Hosts: make(map[string]*config.HostState)}
	}

	log.Printf("INFO: agenkit-runtime serve starting with %d host(s)", len(cfg.Hosts))
	log.Printf("INFO: cluster state: %d known hosts", len(state.Hosts))

	// Build per-host pools from state.
	pools := make(map[string]*pool.Pool, len(state.Hosts))
	for addr, hs := range state.Hosts {
		pools[addr] = pool.NewPool(hs.PoolSize)
	}

	// Build snapshot manager (uses local store).
	snapshotDir := cfg.SnapshotStore
	if snapshotDir == "" {
		snapshotDir = "/var/lib/agenkit/snapshots"
	}
	store, err := snapshot.NewLocalStore(snapshotDir)
	if err != nil {
		log.Printf("WARNING: failed to create snapshot store at %s: %v", snapshotDir, err)
	}
	var snapshotMgr *snapshot.Manager
	if store != nil {
		snapshotMgr = snapshot.NewManager(store, os.TempDir())
	}

	// Create a cancellable context for the monitor and API server.
	monCtx, monCancel := context.WithCancel(ctx)
	defer monCancel()

	// Start the spot monitor (no-op on non-EC2).
	manifestDir := snapshotDir + "/manifests"
	spotMon := migration.NewSpotMonitor(func(deadline time.Time) {
		log.Printf("WARNING: spot interruption detected, deadline %s — initiating migration", deadline)

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
			log.Printf("INFO: spot interruption: no active sessions to migrate")
			return
		}

		// Generate a random migration ID (16 bytes of hex — no external uuid dep).
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			log.Printf("ERROR: migration: failed to generate migration ID: %v", err)
			return
		}
		migrationID := hex.EncodeToString(raw[:])

		// Resolve local host address from config (first host entry as a best-effort default).
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
				log.Printf("ERROR: migration %s: MigrateAll failed: %v", migrationID, err)
				return
			}
			pending := 0
			for _, s := range manifest.Sessions {
				if s.Status == "pending" {
					pending++
				}
			}
			log.Printf("INFO: migration %s complete: %d/%d sessions pending recovery",
				migrationID, pending, len(manifest.Sessions))
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
			log.Printf("WARNING: api server error: %v", err)
		}
	}()

	// Wait for termination signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigCh:
		log.Printf("INFO: received signal %s, shutting down", sig)
	case <-ctx.Done():
	}

	// Persist final state.
	if err := config.SaveState(config.DefaultStatePath, state); err != nil {
		log.Printf("WARNING: failed to save cluster state on shutdown: %v", err)
	}
	return nil
}
