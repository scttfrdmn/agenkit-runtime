// Package internal wires up the cobra command tree for agenkit-runtime.
package internal

import (
	"github.com/spf13/cobra"
)

var cfgFile string

// RootCmd returns the root cobra command.
func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agenkit-runtime",
		Short: "Firecracker microVM pool manager and spot migration daemon for agenkit agents",
		Long: `agenkit-runtime manages pools of Firecracker microVMs that run agenkit agents,
handles spot-eviction migrations, and coordinates cluster scheduling.

Sub-commands:
  serve    Start the host daemon
  recover  Recover sessions from a previous crash or spot eviction
  cluster  Manage the cluster (provision, teardown, status)
  host     Manage individual hosts (add, list, remove, drain, resume)
  snapshot Manage VM snapshots (build, push, pull)`,
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "/etc/agenkit/cluster.yaml",
		"path to cluster config file")

	root.AddCommand(
		serveCmd(),
		recoverCmd(),
		clusterCmd(),
		hostCmd(),
		snapshotCmd(),
	)

	return root
}
