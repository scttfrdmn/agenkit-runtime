package internal

import (
	"fmt"

	"github.com/spf13/cobra"
)

func hostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Manage individual hosts in the cluster",
	}
	cmd.AddCommand(
		hostAddCmd(),
		hostListCmd(),
		hostRemoveCmd(),
		hostDrainCmd(),
		hostResumeCmd(),
	)
	return cmd
}

func hostAddCmd() *cobra.Command {
	var local bool
	var ec2 bool
	cmd := &cobra.Command{
		Use:   "add <addr>",
		Short: "Add a host to the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := args[0]
			switch {
			case local:
				fmt.Printf("Adding local host %s...\n", addr)
			case ec2:
				fmt.Printf("Adding EC2 host %s...\n", addr)
			default:
				return fmt.Errorf("specify --local or --ec2")
			}
			// Full implementation: create Host object, call Provision, update state.
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "add an existing machine via SSH")
	cmd.Flags().BoolVar(&ec2, "ec2", false, "launch a new EC2 instance")
	return cmd
}

func hostListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all hosts in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Hosts:")
			// Full implementation: read state file and print hosts.
			fmt.Println("  (use 'cluster status' for details)")
			return nil
		},
	}
}

func hostRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <addr>",
		Short: "Remove a host from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Removing host %s...\n", args[0])
			return nil
		},
	}
}

func hostDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drain <addr>",
		Short: "Drain all sessions from a host before removing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Draining host %s...\n", args[0])
			return nil
		},
	}
}

func hostResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <addr>",
		Short: "Resume a previously drained host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Resuming host %s...\n", args[0])
			return nil
		},
	}
}
