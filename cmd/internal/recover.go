package internal

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/migration"
)

func recoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recover",
		Short: "Recover agent sessions from a previous crash or spot eviction",
		Long: `recover reads migration manifests from the snapshot store, identifies
sessions that were interrupted but not yet resumed, and reports them.

Sessions with status "pending" are marked "unrecoverable" until the
agenkit-go bridge is available (planned for v0.3.0).

Returns exit code 1 if any sessions could not be recovered.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecover()
		},
	}
}

func runRecover() error {
	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		// Non-fatal: fall back to the default snapshot directory.
		log.Printf("WARNING: recover: failed to load config (%v), using default snapshot dir", err)
		cfg = &config.ClusterConfig{SnapshotStore: "/var/lib/agenkit/snapshots"}
	}

	snapshotDir := cfg.SnapshotStore
	if snapshotDir == "" {
		snapshotDir = "/var/lib/agenkit/snapshots"
	}
	manifestDir := filepath.Join(snapshotDir, "manifests")

	entries, err := os.ReadDir(manifestDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Recovery complete. No pending migrations found.")
			return nil
		}
		return fmt.Errorf("failed to read manifest dir %s: %w", manifestDir, err)
	}

	var manifests []*migration.MigrationManifest
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(manifestDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("WARNING: recover: failed to read %s: %v", path, err)
			continue
		}
		var m migration.MigrationManifest
		if err := json.Unmarshal(data, &m); err != nil {
			log.Printf("WARNING: recover: failed to parse %s: %v", path, err)
			continue
		}
		manifests = append(manifests, &m)
	}

	if len(manifests) == 0 {
		fmt.Println("Recovery complete. No pending migrations found.")
		return nil
	}

	unrecoverable := 0

	// Print header.
	fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
		"MIGRATION ID", "SESSION ID", "CHECKPOINT ID", "STATUS")
	fmt.Println(strings.Repeat("-", 115))

	for _, m := range manifests {
		for i, s := range m.Sessions {
			if s.Status != "pending" {
				fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
					m.MigrationID, s.SessionID, s.CheckpointID, s.Status)
				continue
			}

			// Mark pending sessions as unrecoverable — the agenkit-go bridge is v0.3.0.
			m.Sessions[i].Status = "unrecoverable"
			m.Sessions[i].Error = "ResumeMigrated bridge not available (v0.3.0)"
			unrecoverable++

			fmt.Printf("%-36s  %-20s  %-36s  %-15s\n",
				m.MigrationID, s.SessionID, s.CheckpointID, "unrecoverable")

			// Rewrite manifest atomically to record the updated status.
			if err := rewriteManifest(manifestDir, m); err != nil {
				log.Printf("WARNING: recover: failed to rewrite manifest %s: %v", m.MigrationID, err)
			}
		}
	}

	fmt.Println(strings.Repeat("-", 115))

	if unrecoverable > 0 {
		fmt.Printf("\n%d session(s) marked unrecoverable. ResumeMigrated bridge planned for v0.3.0.\n", unrecoverable)
		return fmt.Errorf("%d session(s) could not be recovered", unrecoverable)
	}

	fmt.Println("\nRecovery complete.")
	return nil
}

// rewriteManifest atomically rewrites m to dir/{migration_id}.json.
func rewriteManifest(dir string, m *migration.MigrationManifest) error {
	m.CompletedAt = time.Now()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	tmp := filepath.Join(dir, m.MigrationID+".json.tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write manifest tmp: %w", err)
	}
	dest := filepath.Join(dir, m.MigrationID+".json")
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename manifest: %w", err)
	}
	return nil
}
