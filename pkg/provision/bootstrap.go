package provision

import (
	"context"
	"fmt"
	"strings"
)

// BootstrapConfig carries parameters for bootstrapping a host.
type BootstrapConfig struct {
	KernelPath          string
	SnapshotStore       string
	AgentRuntimeVersion string
	ExtraEnv            map[string]string
}

// Bootstrap installs Firecracker and agenkit-runtime on the host via conn.
//
// The sequence is:
//  1. Install Firecracker binary
//  2. Install agenkit-runtime daemon
//  3. Write systemd unit and start the service
func Bootstrap(ctx context.Context, conn *SSHConn, cfg BootstrapConfig) error {
	steps := []struct {
		name string
		cmd  string
	}{
		{"update packages", "sudo apt-get update -qq"},
		{"install dependencies", "sudo apt-get install -y -qq curl wget jq"},
		{"download firecracker", buildDownloadFirecrackerCmd()},
		{"install agenkit-runtime", buildInstallCmd(cfg.AgentRuntimeVersion)},
		{"write systemd unit", buildSystemdUnitCmd(cfg)},
		{"enable and start service", "sudo systemctl daemon-reload && sudo systemctl enable --now agenkit-runtime"},
	}

	for _, step := range steps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		out, err := conn.Run(step.cmd)
		if err != nil {
			return fmt.Errorf("bootstrap step %q failed: %w\noutput: %s", step.name, err, out)
		}
	}
	return nil
}

// Unbootstrap stops and removes the agenkit-runtime service from the host.
func Unbootstrap(ctx context.Context, conn *SSHConn) error {
	cmds := []string{
		"sudo systemctl stop agenkit-runtime || true",
		"sudo systemctl disable agenkit-runtime || true",
		"sudo rm -f /etc/systemd/system/agenkit-runtime.service",
		"sudo rm -f /usr/local/bin/agenkit-runtime /usr/local/bin/firecracker",
		"sudo systemctl daemon-reload",
	}
	for _, cmd := range cmds {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if _, err := conn.Run(cmd); err != nil {
			return fmt.Errorf("unbootstrap: %w", err)
		}
	}
	return nil
}

func buildDownloadFirecrackerCmd() string {
	return `set -e
LATEST=$(curl -s https://api.github.com/repos/firecracker-microvm/firecracker/releases/latest | jq -r .tag_name)
ARCH=$(uname -m)
URL="https://github.com/firecracker-microvm/firecracker/releases/download/${LATEST}/firecracker-${LATEST}-${ARCH}.tgz"
curl -L "$URL" | sudo tar -xz -C /usr/local/bin --strip-components=1
sudo chmod +x /usr/local/bin/firecracker`
}

func buildInstallCmd(version string) string {
	if version == "" {
		version = "latest"
	}
	return fmt.Sprintf(`set -e
URL="https://github.com/scttfrdmn/agenkit-runtime/releases/download/%s/agenkit-runtime-$(uname -m)"
sudo curl -L "$URL" -o /usr/local/bin/agenkit-runtime
sudo chmod +x /usr/local/bin/agenkit-runtime`, version)
}

func buildSystemdUnitCmd(cfg BootstrapConfig) string {
	env := ""
	for k, v := range cfg.ExtraEnv {
		env += fmt.Sprintf("Environment=%s=%s\n", k, v)
	}

	unit := fmt.Sprintf(`[Unit]
Description=agenkit-runtime daemon
After=network.target

[Service]
ExecStart=/usr/local/bin/agenkit-runtime serve
Restart=on-failure
RestartSec=5s
%s
[Install]
WantedBy=multi-user.target
`, env)

	escaped := strings.ReplaceAll(unit, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	return fmt.Sprintf(`sudo bash -c 'printf "%s" > /etc/systemd/system/agenkit-runtime.service'`, escaped)
}
