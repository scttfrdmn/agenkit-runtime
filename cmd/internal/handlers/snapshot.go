package handlers

import (
	"context"
	"fmt"

	"github.com/scttfrdmn/agenkit-runtime/pkg/snapshot"
)

// SnapshotBuild sends a build request to the daemon, which triggers Manager.Build().
// The vmPID argument is passed as args["vm_pid"] (parsed to int).
func SnapshotBuild(mgr *snapshot.Manager) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		name := args["name"]
		if name == "" {
			return nil, fmt.Errorf("name argument is required")
		}
		// vmPID 0 triggers the stub path (no real Firecracker integration until v0.3.0).
		dir, err := mgr.Build(ctx, 0, name)
		if err != nil {
			return nil, fmt.Errorf("failed to build snapshot %s: %w", name, err)
		}
		return map[string]string{"snapshot": name, "dir": dir}, nil
	}
}

// SnapshotPush wires Manager.Push for use as an API handler.
func SnapshotPush(mgr *snapshot.Manager) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		name := args["name"]
		dir := args["dir"]
		if name == "" {
			return nil, fmt.Errorf("name argument is required")
		}
		if dir == "" {
			return nil, fmt.Errorf("dir argument is required")
		}
		if err := mgr.Push(ctx, name, dir); err != nil {
			return nil, err
		}
		return map[string]string{"pushed": name}, nil
	}
}

// SnapshotPull wires Manager.Pull for use as an API handler.
func SnapshotPull(mgr *snapshot.Manager) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		name := args["name"]
		dir := args["dir"]
		if name == "" {
			return nil, fmt.Errorf("name argument is required")
		}
		if dir == "" {
			return nil, fmt.Errorf("dir argument is required")
		}
		if err := mgr.Pull(ctx, name, dir); err != nil {
			return nil, err
		}
		return map[string]string{"pulled": name}, nil
	}
}
