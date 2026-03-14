package handlers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/scttfrdmn/agenkit-runtime/pkg/config"
	"github.com/scttfrdmn/agenkit-runtime/pkg/host"
	"github.com/scttfrdmn/agenkit-runtime/pkg/pool"
)

// ClusterStatus returns per-host pool statistics.
func ClusterStatus(state *config.ClusterState, pools map[string]*pool.Pool) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(_ context.Context, _ map[string]string) (interface{}, error) {
		type hostStatus struct {
			*config.HostState
			PoolStats map[pool.VMState]int `json:"pool_stats,omitempty"`
		}
		result := make([]hostStatus, 0, len(state.Hosts))
		for addr, hs := range state.Hosts {
			hs := hostStatus{HostState: hs}
			if p, ok := pools[addr]; ok {
				hs.PoolStats = p.Stats()
			}
			result = append(result, hs)
		}
		return result, nil
	}
}

// ClusterProvision provisions all hosts defined in cfg concurrently and saves state.
func ClusterProvision(cfg *config.ClusterConfig, state *config.ClusterState, statePath string) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, _ map[string]string) (interface{}, error) {
		var mu sync.Mutex
		var wg sync.WaitGroup
		errs := make([]string, 0)

		for _, hcfg := range cfg.Hosts {
			hcfg := hcfg // capture
			wg.Add(1)
			go func() {
				defer wg.Done()

				var h host.Host
				switch hcfg.Type {
				case "local":
					h = host.NewLocalHost(hcfg.Addr, hcfg.User, hcfg.PoolSize)
				case "ec2":
					ec2h, err := host.NewEC2Host(host.EC2HostConfig{
						Region:       hcfg.Region,
						InstanceType: hcfg.InstanceType,
						Spot:         hcfg.Spot,
						PoolSize:     hcfg.PoolSize,
					})
					if err != nil {
						mu.Lock()
						errs = append(errs, fmt.Sprintf("create ec2 host: %v", err))
						mu.Unlock()
						return
					}
					h = ec2h
				default:
					mu.Lock()
					errs = append(errs, fmt.Sprintf("unknown host type %q", hcfg.Type))
					mu.Unlock()
					return
				}

				provCfg := host.ProvisionConfig{
					KernelPath:    cfg.KernelPath,
					SnapshotStore: cfg.SnapshotStore,
				}
				if err := h.Provision(ctx, provCfg); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Sprintf("provision %s: %v", h.Address(), err))
					mu.Unlock()
					return
				}

				hs := &config.HostState{
					Address:       h.Address(),
					PoolSize:      h.PoolSize(),
					ProvisionedAt: time.Now().UTC(),
				}
				if ec2h, ok := h.(*host.EC2Host); ok {
					hs.InstanceID = ec2h.InstanceID()
				}
				mu.Lock()
				state.Hosts[h.Address()] = hs
				mu.Unlock()
			}()
		}
		wg.Wait()

		if len(errs) > 0 {
			// Save partial state before returning error.
			_ = config.SaveState(statePath, state)
			return nil, fmt.Errorf("provision errors: %v", errs)
		}
		if err := config.SaveState(statePath, state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
		return map[string]int{"provisioned": len(state.Hosts)}, nil
	}
}

// ClusterTeardown drains and removes all hosts. Accepts force=true to skip graceful drain.
func ClusterTeardown(state *config.ClusterState, pools map[string]*pool.Pool, statePath string) func(ctx context.Context, args map[string]string) (interface{}, error) {
	return func(ctx context.Context, args map[string]string) (interface{}, error) {
		force := args["force"] == "true"
		drainMode := pool.DrainGraceful
		if force {
			drainMode = pool.DrainForce
		}

		for addr, p := range pools {
			p.Drain(ctx, drainMode)
			delete(state.Hosts, addr)
		}
		if err := config.SaveState(statePath, state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
		return map[string]string{"status": "torn down"}, nil
	}
}
