package internal

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/scttfrdmn/agenkit-runtime/pkg/api"
	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
)

func clusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage the agenkit-runtime cluster",
	}
	cmd.AddCommand(
		clusterProvisionCmd(),
		clusterTeardownCmd(),
		clusterStatusCmd(),
	)
	return cmd
}

func clusterProvisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provision",
		Short: "Provision all hosts defined in the cluster config",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := api.SendRequest(cmd.Context(), api.Request{Command: "cluster.provision"})
			if err != nil {
				// Daemon not running: provision directly from config (best-effort).
				cfg, cerr := config.LoadConfig(cfgFile)
				if cerr != nil {
					return fmt.Errorf("daemon unavailable (%v) and cannot load config: %w", err, cerr)
				}
				fmt.Printf("Daemon unavailable; provisioning %d host(s) from %s without daemon...\n",
					len(cfg.Hosts), cfgFile)
				for i, h := range cfg.Hosts {
					fmt.Printf("  [%d] type=%s poolSize=%d\n", i+1, h.Type, h.PoolSize)
				}
				fmt.Println("Run 'agenkit-runtime serve' to start the daemon.")
				return nil
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			b, _ := json.MarshalIndent(resp.Data, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	}
}

func clusterTeardownCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Terminate all cluster resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]string{}
			if force {
				reqArgs["force"] = "true"
			}
			resp, err := api.SendRequest(cmd.Context(), api.Request{
				Command: "cluster.teardown",
				Args:    reqArgs,
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			fmt.Println("Cluster torn down.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "force teardown without graceful drain")
	return cmd
}

func clusterStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := api.SendRequest(cmd.Context(), api.Request{Command: "cluster.status"})
			if err != nil {
				// Fall back to state file.
				state, serr := config.LoadState(config.DefaultStatePath)
				if serr != nil {
					fmt.Printf("No cluster state found at %s.\n", config.DefaultStatePath)
					return nil //nolint:nilerr
				}
				printClusterState(state)
				return nil
			}
			if !resp.OK {
				return fmt.Errorf("daemon error: %s", resp.Error)
			}
			b, _ := json.MarshalIndent(resp.Data, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	}
}

func printClusterState(state *config.ClusterState) {
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
		fmt.Printf("  %s%s  poolSize=%d  provisioned=%s\n",
			addr, idStr, h.PoolSize,
			h.ProvisionedAt.Format("2006-01-02 15:04:05"))
	}
}
