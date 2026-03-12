package internal

import (
	"fmt"

	"github.com/spf13/cobra"
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
		Short: "Build a snapshot from a running VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Building snapshot %s...\n", args[0])
			// Full implementation: connect to daemon via Unix socket, request snapshot build.
			return nil
		},
	}
}

func snapshotPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push <name>",
		Short: "Push a local snapshot to the configured snapshot store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Pushing snapshot %s...\n", args[0])
			return nil
		},
	}
}

func snapshotPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull <name>",
		Short: "Pull a snapshot from the store to the local host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Pulling snapshot %s...\n", args[0])
			return nil
		},
	}
}
