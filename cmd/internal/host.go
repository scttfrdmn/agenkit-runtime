package internal

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/api"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
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
	var user string
	var region string
	var instanceType string
	var ami string
	var keyName string
	var spot bool

	cmd := &cobra.Command{
		Use:   "add <addr>",
		Short: "Add a host to the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := args[0]
			reqArgs := map[string]string{"addr": addr}
			switch {
			case local:
				reqArgs["type"] = "local"
				if user != "" {
					reqArgs["user"] = user
				}
			case ec2:
				reqArgs["type"] = "ec2"
				reqArgs["region"] = region
				reqArgs["instance_type"] = instanceType
				reqArgs["ami"] = ami
				reqArgs["key_name"] = keyName
				if spot {
					reqArgs["spot"] = "true"
				}
			default:
				return fmt.Errorf("specify --local or --ec2")
			}

			resp, err := api.SendRequest(cmd.Context(), api.Request{Command: "host.add", Args: reqArgs})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			fmt.Printf("Host added: %s\n", addr)
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "add an existing machine via SSH")
	cmd.Flags().BoolVar(&ec2, "ec2", false, "launch a new EC2 instance")
	cmd.Flags().StringVar(&user, "user", "", "SSH user (local hosts)")
	cmd.Flags().StringVar(&region, "region", "", "AWS region (EC2 hosts)")
	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type")
	cmd.Flags().StringVar(&ami, "ami", "", "AMI ID (EC2 hosts)")
	cmd.Flags().StringVar(&keyName, "key-name", "", "EC2 key pair name")
	cmd.Flags().BoolVar(&spot, "spot", false, "use spot instance (EC2 hosts)")
	return cmd
}

func hostListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all hosts in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := api.SendRequest(cmd.Context(), api.Request{Command: "host.list"})
			if err != nil {
				// Fall back to reading state file directly when daemon is not running.
				state, serr := config.LoadState(config.DefaultStatePath)
				if serr != nil {
					return fmt.Errorf("daemon unavailable (%v) and no state file: %w", err, serr)
				}
				printHostState(state)
				return nil
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			// Re-encode data for display.
			b, _ := json.MarshalIndent(resp.Data, "", "  ")
			fmt.Println(string(b))
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
			resp, err := api.SendRequest(cmd.Context(), api.Request{
				Command: "host.remove",
				Args:    map[string]string{"addr": args[0]},
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			fmt.Printf("Host removed: %s\n", args[0])
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
			resp, err := api.SendRequest(cmd.Context(), api.Request{
				Command: "host.drain",
				Args:    map[string]string{"addr": args[0]},
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			fmt.Printf("Host drained: %s\n", args[0])
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
			resp, err := api.SendRequest(cmd.Context(), api.Request{
				Command: "host.resume",
				Args:    map[string]string{"addr": args[0]},
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			fmt.Printf("Host resumed: %s\n", args[0])
			return nil
		},
	}
}

func printHostState(state *config.ClusterState) {
	fmt.Printf("Cluster state (updated %s):\n", state.UpdatedAt.Format("2006-01-02 15:04:05"))
	if len(state.Hosts) == 0 {
		fmt.Println("  No hosts provisioned.")
		return
	}
	for addr, h := range state.Hosts {
		idStr := ""
		if h.InstanceID != "" {
			idStr = " (" + h.InstanceID + ")"
		}
		drainStr := ""
		if h.Drained {
			drainStr = "  [drained]"
		}
		fmt.Printf("  %s%s  poolSize=%d  provisioned=%s%s\n",
			addr, idStr, h.PoolSize,
			h.ProvisionedAt.Format("2006-01-02 15:04:05"),
			drainStr)
	}
}
