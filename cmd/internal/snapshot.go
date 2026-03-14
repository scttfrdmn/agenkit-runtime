package internal

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/api"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/snapshot"
)

func snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage Firecracker VM snapshots",
	}
	cmd.AddCommand(
		snapshotBuildCmd(),
		snapshotPushCmd(),
		snapshotPullCmd(),
	)
	return cmd
}

func snapshotBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build <name>",
		Short: "Build a snapshot from a running VM (requires daemon)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := api.SendRequest(cmd.Context(), api.Request{
				Command: "snapshot.build",
				Args:    map[string]string{"name": args[0]},
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			fmt.Printf("Snapshot built: %s\n", args[0])
			return nil
		},
	}
}

func snapshotPushCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "push <name>",
		Short: "Push a local snapshot to the configured snapshot store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			return runSnapshotPush(cmd.Context(), name, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "local snapshot directory to push")
	return cmd
}

func snapshotPullCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "pull <name>",
		Short: "Pull a snapshot from the store to the local host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			return runSnapshotPull(cmd.Context(), name, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "local directory to pull snapshot into")
	return cmd
}

// runSnapshotPush tries the daemon first, falls back to local Manager.Push.
func runSnapshotPush(ctx context.Context, name, dir string) error {
	resp, err := api.SendRequest(ctx, api.Request{
		Command: "snapshot.push",
		Args:    map[string]string{"name": name, "dir": dir},
	})
	if err == nil {
		if !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}
		fmt.Printf("Snapshot pushed: %s\n", name)
		return nil
	}

	// Daemon unavailable — run directly.
	mgr, err2 := localSnapshotManager()
	if err2 != nil {
		return fmt.Errorf("daemon unavailable (%v) and cannot create local manager: %w", err, err2)
	}
	if err2 := mgr.Push(ctx, name, dir); err2 != nil {
		return err2
	}
	fmt.Printf("Snapshot pushed: %s\n", name)
	return nil
}

// runSnapshotPull tries the daemon first, falls back to local Manager.Pull.
func runSnapshotPull(ctx context.Context, name, dir string) error {
	resp, err := api.SendRequest(ctx, api.Request{
		Command: "snapshot.pull",
		Args:    map[string]string{"name": name, "dir": dir},
	})
	if err == nil {
		if !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}
		fmt.Printf("Snapshot pulled: %s\n", name)
		return nil
	}

	// Daemon unavailable — run directly.
	mgr, err2 := localSnapshotManager()
	if err2 != nil {
		return fmt.Errorf("daemon unavailable (%v) and cannot create local manager: %w", err, err2)
	}
	if err2 := mgr.Pull(ctx, name, dir); err2 != nil {
		return err2
	}
	fmt.Printf("Snapshot pulled: %s\n", name)
	return nil
}

func localSnapshotManager() (*snapshot.Manager, error) {
	cfg, err := config.LoadConfig(cfgFile)
	storeDir := "/var/lib/agenkit/snapshots"
	if err == nil && cfg.SnapshotStore != "" {
		storeDir = cfg.SnapshotStore
	}
	store, err := snapshot.NewLocalStore(storeDir)
	if err != nil {
		return nil, err
	}
	return snapshot.NewManager(store, os.TempDir()), nil
}
