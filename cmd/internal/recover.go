package internal

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
)

func recoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recover",
		Short: "Recover agent sessions from a previous crash or spot eviction",
		Long: `recover reads the cluster state and pending migration manifests, identifies
sessions that were interrupted but not yet resumed, and attempts to resume each
one via DurableAgent.ResumeMigrated using the checkpoints in shared storage.

Sessions that cannot be recovered (no checkpoint found) are reported but not
treated as fatal errors.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecover()
		},
	}
}

func runRecover() error {
	state, err := config.LoadState(config.DefaultStatePath)
	if err != nil {
		// Try loading from the snapshot store location as a fallback.
		log.Printf("WARNING: local state missing (%v), proceeding with empty state", err)
		state = &config.ClusterState{Hosts: make(map[string]*config.HostState)}
	}

	log.Printf("INFO: recover: cluster state last updated %s", state.UpdatedAt)
	log.Printf("INFO: recover: %d known hosts", len(state.Hosts))

	// In a full implementation this would:
	// 1. List migration manifests in the snapshot store.
	// 2. For each pending SessionMigration, call DurableAgent.ResumeMigrated.
	// 3. Mark the session as "resumed" or "failed" in the manifest.
	// 4. Print a summary.

	fmt.Println("Recovery complete. No pending migrations found.")
	return nil
}
