// Package config handles cluster configuration parsing and state persistence.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultConfigPath is the default location for the cluster config file.
	DefaultConfigPath = "/etc/agenkit/cluster.yaml"
	// DefaultStatePath is where the cluster state is persisted between restarts.
	DefaultStatePath = "/etc/agenkit/cluster-state.json"
)

// ClusterConfig is the top-level cluster configuration.
type ClusterConfig struct {
	// Hosts is the list of machines in the cluster.
	Hosts []HostConfig `yaml:"hosts"`
	// SnapshotStore is the path or URI of the shared snapshot store.
	// May be a local path or an S3 URL (s3://bucket/prefix).
	SnapshotStore string `yaml:"snapshot_store"`
	// KernelPath is the path to the guest vmlinux on each host.
	KernelPath string `yaml:"kernel_path"`
}

// HostConfig describes a single host in the cluster.
type HostConfig struct {
	// Type is "local" or "ec2".
	Type string `yaml:"type"`

	// --- Local host fields ---
	// Addr is the hostname or IP of a local host.
	Addr string `yaml:"addr,omitempty"`
	// User is the SSH user for local hosts.
	User string `yaml:"user,omitempty"`

	// --- EC2 host fields ---
	// Region is the AWS region for EC2 hosts.
	Region string `yaml:"region,omitempty"`
	// InstanceType is the EC2 instance type.
	InstanceType string `yaml:"instance_type,omitempty"`
	// Spot, if true, requests a spot instance.
	Spot bool `yaml:"spot,omitempty"`
	// Auto enables automatic scaling for this host group.
	Auto bool `yaml:"auto,omitempty"`
	// ScaleUpThreshold is the pool utilisation (0-1) above which a new instance is launched.
	ScaleUpThreshold float64 `yaml:"scale_up_threshold,omitempty"`
	// ScaleDownAfter is the idle duration before an instance is terminated.
	ScaleDownAfter time.Duration `yaml:"scale_down_after,omitempty"`
	// MaxInstances limits how many EC2 instances of this type may run concurrently.
	MaxInstances int `yaml:"max_instances,omitempty"`

	// PoolSize is the number of Firecracker VM slots per host.
	PoolSize int `yaml:"pool_size"`
}

// LoadConfig reads and parses a ClusterConfig from a YAML file.
func LoadConfig(path string) (*ClusterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}
	var cfg ClusterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}
	return &cfg, nil
}

// ClusterState records the runtime state of the cluster, persisted to disk
// so the daemon can reconcile after a restart.
type ClusterState struct {
	// Hosts maps host address to their persisted host state.
	Hosts map[string]*HostState `json:"hosts"`
	// UpdatedAt is the last time this state was written.
	UpdatedAt time.Time `json:"updated_at"`
}

// HostState records the runtime state of a single host.
type HostState struct {
	// InstanceID is the EC2 instance ID (empty for local hosts).
	InstanceID string `json:"instance_id,omitempty"`
	// Address is the current SSH address.
	Address string `json:"address"`
	// PoolSize is the configured VM slot count.
	PoolSize int `json:"pool_size"`
	// ProvisionedAt is when the host was last bootstrapped.
	ProvisionedAt time.Time `json:"provisioned_at"`
}

// LoadState reads the cluster state from path.
// Returns an empty state if the file does not exist.
func LoadState(path string) (*ClusterState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ClusterState{Hosts: make(map[string]*HostState)}, nil
		}
		return nil, fmt.Errorf("failed to read state file %s: %w", path, err)
	}
	var state ClusterState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file %s: %w", path, err)
	}
	return &state, nil
}

// SaveState writes state to path atomically.
func SaveState(path string, state *ClusterState) error {
	state.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialise cluster state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	return nil
}
