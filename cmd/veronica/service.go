package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// serviceLabel is the reverse-DNS identifier for the Veronica background service.
const serviceLabel = "com.mfenderov.veronica"

// serviceUnitName is the systemd unit name for the Veronica background service.
const serviceUnitName = "veronica.service"

func newInstallServiceCmd() *cobra.Command {
	var cfgPath string
	var uninstall bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install-service",
		Short: "Install Veronica as a background service (systemd/launchd)",
		Long:  "Write a systemd user unit (Linux) or LaunchAgent plist (macOS) so 'veronica serve' starts automatically, then enable and start it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultCfg, _ := defaultPaths()
			if cfgPath == "" {
				cfgPath = defaultCfg
			}
			return runInstallService(runtime.GOOS, cfgPath, uninstall, dryRun, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file (default: ~/.config/veronica/config.yaml)")
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "stop, disable and remove the background service")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the service file without writing or enabling it")
	return cmd
}

func runInstallService(goos, cfgPath string, uninstall, dryRun bool, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return installServiceFlow(goos, home, resolveExecPath(), cfgPath, uninstall, dryRun, runCommand, out)
}

func installServiceFlow(goos, home, execPath, cfgPath string, uninstall, dryRun bool, run func(string, ...string) error, out io.Writer) error {
	target, err := serviceFilePath(goos, home)
	if err != nil {
		return err
	}
	if uninstall {
		if err := disableService(goos, target, run); err != nil {
			return err
		}
		fmt.Fprintf(out, "Removed background service (%s)\n", target)
		return nil
	}

	content := renderServiceFile(goos, execPath, cfgPath, os.Getenv("PATH"))
	if err := writeServiceFile(target, content, dryRun, out); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	if err := enableService(goos, run); err != nil {
		return err
	}
	fmt.Fprintf(out, "Installed and started background service (%s)\n", target)
	return nil
}

func renderServiceFile(goos, execPath, cfgPath, pathEnv string) string {
	if goos == "darwin" {
		return renderLaunchAgent(serviceLabel, execPath, cfgPath, pathEnv)
	}
	return renderSystemdUnit(execPath, cfgPath, pathEnv)
}

func renderSystemdUnit(execPath, cfgPath, pathEnv string) string {
	return `[Unit]
Description=Veronica MCP gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=PATH=` + pathEnv + `
ExecStart=` + quotePath(execPath) + ` serve --config ` + quotePath(cfgPath) + `
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}

func renderLaunchAgent(label, execPath, cfgPath, pathEnv string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + execPath + `</string>
		<string>serve</string>
		<string>--config</string>
		<string>` + cfgPath + `</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + pathEnv + `</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`
}

func quotePath(p string) string {
	if strings.ContainsAny(p, " \t\"") {
		return strconv.Quote(p)
	}
	return p
}

func serviceFilePath(goos, home string) (string, error) {
	switch goos {
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", serviceUnitName), nil
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"), nil
	default:
		return "", fmt.Errorf("unsupported platform for background service: %s", goos)
	}
}

func writeServiceFile(path, content string, dryRun bool, out io.Writer) error {
	if dryRun {
		fmt.Fprint(out, content)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create service directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}
	return nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func launchTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), serviceLabel)
}

func resolveExecPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return "veronica"
	}
	return execPath
}

func enableService(goos string, run func(string, ...string) error) error {
	switch goos {
	case "linux":
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return run("systemctl", "--user", "enable", "--now", serviceUnitName)
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to resolve home directory: %w", err)
		}
		target, err := serviceFilePath(goos, home)
		if err != nil {
			return err
		}
		_ = run("launchctl", "bootout", launchTarget())
		return run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), target)
	default:
		return fmt.Errorf("unsupported platform for background service: %s", goos)
	}
}

func disableService(goos, target string, run func(string, ...string) error) error {
	switch goos {
	case "linux":
		if err := run("systemctl", "--user", "disable", "--now", serviceUnitName); err != nil {
			return err
		}
	case "darwin":
		_ = run("launchctl", "bootout", launchTarget())
	default:
		return fmt.Errorf("unsupported platform for background service: %s", goos)
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file: %w", err)
	}
	return nil
}
