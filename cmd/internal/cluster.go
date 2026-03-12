package internal

import (
	"fmt"

	"github.com/spf13/cobra"
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
			cfg, err := config.LoadConfig(cfgFile)
			if err != nil {
				return err
			}
			fmt.Printf("Provisioning %d host(s) from %s...\n", len(cfg.Hosts), cfgFile)
			// Full implementation would iterate cfg.Hosts, create Host objects, and call Provision.
			for i, h := range cfg.Hosts {
				fmt.Printf("  [%d] type=%s poolSize=%d\n", i+1, h.Type, h.PoolSize)
			}
			fmt.Println("Done. Run 'agenkit-runtime serve' to start the daemon.")
			return nil
		},
	}
}

func clusterTeardownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "teardown",
		Short: "Terminate all cluster resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Tearing down cluster...")
			// Full implementation would drain all VMs, then call Terminate on EC2 hosts.
			return nil
		},
	}
}

func clusterStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := config.LoadState(config.DefaultStatePath)
			if err != nil {
				fmt.Printf("No cluster state found at %s.\n", config.DefaultStatePath)
				return nil //nolint:nilerr
			}
			fmt.Printf("Cluster state (updated %s):\n", state.UpdatedAt.Format("2006-01-02 15:04:05"))
			if len(state.Hosts) == 0 {
				fmt.Println("  No hosts provisioned.")
				return nil
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
			return nil
		},
	}
}
