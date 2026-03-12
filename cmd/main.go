// Command agenkit-runtime is the host-side daemon and CLI for managing Firecracker
// microVM pools, spot migration, and cluster scheduling for agenkit agents.
package main

import (
	"os"

	"github.com/scttfrdmn/agenkit-runtime/cmd/internal"
)

func main() {
	if err := internal.RootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
