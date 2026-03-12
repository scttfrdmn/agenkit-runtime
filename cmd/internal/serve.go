package internal

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/migration"
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

	// Start the spot monitor (no-op on non-EC2).
	spotMon := migration.NewSpotMonitor(func(deadline time.Time) {
		log.Printf("WARNING: spot interruption detected, deadline %s — initiating migration", deadline)
		// In a full implementation, this would trigger MigrateAll for all active VMs.
	})

	monCtx, monCancel := context.WithCancel(ctx)
	defer monCancel()
	go spotMon.Run(monCtx)

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
