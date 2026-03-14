package host

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/scttfrdmn/agenkit-runtime/pkg/provision"
)

// EC2HostConfig holds parameters for launching an EC2 spot instance.
type EC2HostConfig struct {
	Region       string
	InstanceType string
	AMI          string // must have agenkit-runtime pre-installed or support bootstrap
	KeyName      string // EC2 key pair name for SSH access
	SSHUser      string // default "ec2-user"
	Spot         bool
	PoolSize     int
	SubnetID     string // optional
	SecurityGroups []string
}

// EC2Host launches an EC2 (optionally spot) instance and manages Firecracker VMs on it.
type EC2Host struct {
	cfg        EC2HostConfig
	instanceID string
	publicDNS  string
	ec2Client  *ec2.Client
}

// NewEC2Host creates an EC2Host. Call Provision to actually launch the instance.
func NewEC2Host(cfg EC2HostConfig) (*EC2Host, error) {
	if cfg.SSHUser == "" {
		cfg.SSHUser = "ec2-user"
	}
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	return &EC2Host{
		cfg:       cfg,
		ec2Client: ec2.NewFromConfig(awsCfg),
	}, nil
}

// Provision launches the EC2 instance and bootstraps it.
func (h *EC2Host) Provision(ctx context.Context, provCfg ProvisionConfig) error {
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(h.cfg.AMI),
		InstanceType: types.InstanceType(h.cfg.InstanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		KeyName:      aws.String(h.cfg.KeyName),
	}

	if len(h.cfg.SecurityGroups) > 0 {
		input.SecurityGroupIds = h.cfg.SecurityGroups
	}
	if h.cfg.SubnetID != "" {
		input.SubnetId = aws.String(h.cfg.SubnetID)
	}

	if h.cfg.Spot {
		input.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{
			MarketType: types.MarketTypeSpot,
		}
	}

	out, err := h.ec2Client.RunInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("ec2 RunInstances failed: %w", err)
	}
	if len(out.Instances) == 0 {
		return fmt.Errorf("ec2 RunInstances returned no instances")
	}

	h.instanceID = aws.ToString(out.Instances[0].InstanceId)

	// Wait for the instance to reach running state.
	if err := h.waitUntilRunning(ctx); err != nil {
		return err
	}

	// Retrieve public DNS.
	desc, err := h.ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{h.instanceID},
	})
	if err != nil {
		return fmt.Errorf("failed to describe instance: %w", err)
	}
	if len(desc.Reservations) > 0 && len(desc.Reservations[0].Instances) > 0 {
		h.publicDNS = aws.ToString(desc.Reservations[0].Instances[0].PublicDnsName)
	}

	// Bootstrap via SSH.
	conn, err := provision.WaitForSSH(ctx, h.cfg.SSHUser, h.publicDNS, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("ssh connect to new instance: %w", err)
	}
	defer conn.Close()

	return provision.Bootstrap(ctx, conn, provision.BootstrapConfig{
		KernelPath:          provCfg.KernelPath,
		SnapshotStore:       provCfg.SnapshotStore,
		AgentRuntimeVersion: provCfg.AgentRuntimeVersion,
		ExtraEnv:            provCfg.ExtraEnv,
	})
}

// waitUntilRunning polls DescribeInstances until the instance is running.
func (h *EC2Host) waitUntilRunning(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		out, err := h.ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{h.instanceID},
		})
		if err != nil {
			return fmt.Errorf("describe instances: %w", err)
		}
		if len(out.Reservations) > 0 && len(out.Reservations[0].Instances) > 0 {
			state := out.Reservations[0].Instances[0].State
			if state != nil && state.Name == types.InstanceStateNameRunning {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for instance %s to start", h.instanceID)
}

// InstanceID returns the EC2 instance ID (empty until Provision is called).
func (h *EC2Host) InstanceID() string { return h.instanceID }

// Address returns the public DNS name of the instance.
func (h *EC2Host) Address() string { return h.publicDNS }

// PoolSize returns the number of VM slots configured.
func (h *EC2Host) PoolSize() int { return h.cfg.PoolSize }

// Type returns "ec2".
func (h *EC2Host) Type() string { return "ec2" }

// Terminate terminates the EC2 instance.
func (h *EC2Host) Terminate(ctx context.Context) error {
	if h.instanceID == "" {
		return nil
	}
	_, err := h.ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{h.instanceID},
	})
	if err != nil {
		return fmt.Errorf("failed to terminate instance %s: %w", h.instanceID, err)
	}
	return nil
}
