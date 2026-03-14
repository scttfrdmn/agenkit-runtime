// Package handlers contains daemon-side command handlers for the agenkit-runtime API.
package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/host"
	"github.com/scttfrdmn/agenkit-runtime/pkg/pool"
)

// HostAdd provisions a new host and adds it to the cluster state.
func HostAdd(cfg *config.ClusterConfig, state *config.ClusterState, statePath string) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		addr := args["addr"]
		if addr == "" {
			return nil, fmt.Errorf("addr argument is required")
		}
		hostType := args["type"]
		if hostType == "" {
			hostType = "local"
		}

		var h host.Host
		switch hostType {
		case "local":
			user := args["user"]
			if user == "" {
				user = "root"
			}
			h = host.NewLocalHost(addr, user, 4)
		case "ec2":
			ec2h, err := host.NewEC2Host(host.EC2HostConfig{
				Region:       args["region"],
				InstanceType: args["instance_type"],
				AMI:          args["ami"],
				KeyName:      args["key_name"],
				PoolSize:     4,
				Spot:         args["spot"] == "true",
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create EC2 host: %w", err)
			}
			h = ec2h
		default:
			return nil, fmt.Errorf("unknown host type %q: must be local or ec2", hostType)
		}

		provCfg := host.ProvisionConfig{
			KernelPath:    cfg.KernelPath,
			SnapshotStore: cfg.SnapshotStore,
		}
		if err := h.Provision(ctx, provCfg); err != nil {
			return nil, fmt.Errorf("failed to provision host %s: %w", addr, err)
		}

		hs := &config.HostState{
			Address:       h.Address(),
			PoolSize:      h.PoolSize(),
			ProvisionedAt: time.Now().UTC(),
		}
		if ec2h, ok := h.(*host.EC2Host); ok {
			hs.InstanceID = ec2h.InstanceID()
		}
		state.Hosts[h.Address()] = hs
		if err := config.SaveState(statePath, state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
		return hs, nil
	}
}

// HostList returns all known hosts from state.
func HostList(state *config.ClusterState) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(_ context.Context, _ map[string]string) (interface{}, error) {
		hosts := make([]*config.HostState, 0, len(state.Hosts))
		for _, hs := range state.Hosts {
			hosts = append(hosts, hs)
		}
		return hosts, nil
	}
}

// HostRemove terminates a host and removes it from the cluster state.
func HostRemove(state *config.ClusterState, pools map[string]*pool.Pool, statePath string) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		addr := args["addr"]
		if addr == "" {
			return nil, fmt.Errorf("addr argument is required")
		}
		if _, ok := state.Hosts[addr]; !ok {
			return nil, fmt.Errorf("host %s not found in cluster state", addr)
		}

		// Drain the host's pool if it exists.
		if p, ok := pools[addr]; ok {
			p.Drain(ctx, pool.DrainForce)
		}

		delete(state.Hosts, addr)
		if err := config.SaveState(statePath, state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
		return map[string]string{"removed": addr}, nil
	}
}

// HostDrain gracefully drains all sessions from a host's pool.
func HostDrain(pools map[string]*pool.Pool) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		addr := args["addr"]
		if addr == "" {
			return nil, fmt.Errorf("addr argument is required")
		}
		p, ok := pools[addr]
		if !ok {
			return nil, fmt.Errorf("no pool found for host %s", addr)
		}
		p.Drain(ctx, pool.DrainGraceful)
		return map[string]string{"drained": addr}, nil
	}
}

// HostResume marks a previously drained host as active.
func HostResume(state *config.ClusterState, statePath string) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(_ context.Context, args map[string]string) (interface{}, error) {
		addr := args["addr"]
		if addr == "" {
			return nil, fmt.Errorf("addr argument is required")
		}
		hs, ok := state.Hosts[addr]
		if !ok {
			return nil, fmt.Errorf("host %s not found in cluster state", addr)
		}
		hs.Drained = false
		if err := config.SaveState(statePath, state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
		return hs, nil
	}
}
