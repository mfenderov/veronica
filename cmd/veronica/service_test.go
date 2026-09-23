package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewInstallServiceCmd_Registered(t *testing.T) {
	t.Parallel()

	rootCmd := newRootCmd()
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "install-service" {
			found = true
			for _, flag := range []string{"config", "uninstall", "dry-run"} {
				if c.Flag(flag) == nil {
					t.Errorf("expected --%s flag to exist", flag)
				}
			}
		}
	}
	if !found {
		t.Fatal("expected install-service command to exist")
	}
}

func TestRenderSystemdUnit_ExecStartAndRestart(t *testing.T) {
	t.Parallel()

	unit := renderSystemdUnit("/home/u/.local/bin/veronica", "/home/u/.config/veronica/config.yaml", "/usr/bin:/bin:/home/u/.local/bin")

	for _, want := range []string{
		"ExecStart=/home/u/.local/bin/veronica serve --config /home/u/.config/veronica/config.yaml",
		"Restart=on-failure",
		"WantedBy=default.target",
		"Description=Veronica MCP gateway",
		"Environment=PATH=/usr/bin:/bin:/home/u/.local/bin",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("expected unit to contain %q, got:\n%s", want, unit)
		}
	}
}

func TestRenderSystemdUnit_QuotesPathsWithSpaces(t *testing.T) {
	t.Parallel()

	unit := renderSystemdUnit("/home/u/my bins/veronica", "/home/u/cfg dir/config.yaml", "/usr/bin:/bin")

	if !strings.Contains(unit, `ExecStart="/home/u/my bins/veronica" serve --config "/home/u/cfg dir/config.yaml"`) {
		t.Errorf("expected quoted paths in ExecStart, got:\n%s", unit)
	}
}

func TestRenderLaunchAgent_ProgramArguments(t *testing.T) {
	t.Parallel()

	plist := renderLaunchAgent("com.mfenderov.veronica", "/opt/homebrew/bin/veronica", "/Users/u/.config/veronica/config.yaml", "/usr/bin:/bin:/opt/homebrew/bin")

	for _, want := range []string{
		"<string>com.mfenderov.veronica</string>",
		"<string>/opt/homebrew/bin/veronica</string>",
		"<string>serve</string>",
		"<string>--config</string>",
		"<string>/Users/u/.config/veronica/config.yaml</string>",
		"<key>PATH</key>",
		"<string>/usr/bin:/bin:/opt/homebrew/bin</string>",
		"<true/>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("expected plist to contain %q, got:\n%s", want, plist)
		}
	}
}

func TestServiceFilePath_Platforms(t *testing.T) {
	t.Parallel()

	path, err := serviceFilePath("linux", "/home/u")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/home/u/.config/systemd/user/veronica.service" {
		t.Fatalf("unexpected linux path: %s", path)
	}

	path, err = serviceFilePath("darwin", "/Users/u")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/Users/u/Library/LaunchAgents/com.mfenderov.veronica.plist" {
		t.Fatalf("unexpected darwin path: %s", path)
	}

	if _, err := serviceFilePath("windows", "C:\\u"); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

func TestWriteServiceFile_DryRunPrintsWithoutWriting(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	target := filepath.Join(t.TempDir(), "veronica.service")

	if err := writeServiceFile(target, "unit-content", true, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.String() != "unit-content" {
		t.Fatalf("expected content printed, got %q", out.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("expected no file written in dry-run mode")
	}
}

func TestWriteServiceFile_WritesFile(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	target := filepath.Join(t.TempDir(), "nested", "veronica.service")

	if err := writeServiceFile(target, "unit-content", false, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected file written: %v", err)
	}
	if string(data) != "unit-content" {
		t.Fatalf("unexpected file content: %q", data)
	}
}

func TestEnableService_LinuxRunsSystemctl(t *testing.T) {
	t.Parallel()

	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	if err := enableService("linux", run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", "veronica.service"},
	}
	if len(calls) != len(want) {
		t.Fatalf("expected %d calls, got %v", len(want), calls)
	}
	for i := range want {
		if strings.Join(calls[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("call %d: expected %q, got %q", i, want[i], calls[i])
		}
	}
}

func TestEnableService_DarwinRunsLaunchctl(t *testing.T) {
	t.Parallel()

	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	if err := enableService("darwin", run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(calls[0], " ")
	if calls[0][0] != "launchctl" || !strings.Contains(joined, "bootout") {
		t.Errorf("expected launchctl bootout first, got %v", calls)
	}
	joined = strings.Join(calls[1], " ")
	if calls[1][0] != "launchctl" || !strings.Contains(joined, "bootstrap") || !strings.Contains(joined, "com.mfenderov.veronica.plist") {
		t.Errorf("expected launchctl bootstrap of plist, got %v", calls)
	}
}

func TestEnableService_RunnerErrorPropagates(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("reload failed")
	run := func(name string, args ...string) error { return sentinel }

	if err := enableService("linux", run); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestDisableService_StopsAndRemoves(t *testing.T) {
	t.Parallel()

	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	target := filepath.Join(t.TempDir(), "veronica.service")
	if err := os.WriteFile(target, []byte("unit"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := disableService("linux", target, run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(calls[0], " "); got != "systemctl --user disable --now veronica.service" {
		t.Errorf("expected systemctl disable, got %q", got)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("expected service file removed")
	}
}

func TestDisableService_DarwinBootout(t *testing.T) {
	t.Parallel()

	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	target := filepath.Join(t.TempDir(), "com.mfenderov.veronica.plist")
	if err := os.WriteFile(target, []byte("plist"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := disableService("darwin", target, run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls[0][0] != "launchctl" || !strings.Contains(strings.Join(calls[0], " "), "bootout") {
		t.Errorf("expected launchctl bootout, got %v", calls)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("expected plist removed")
	}
}

func TestInstallServiceFlow_InstallWritesAndEnables(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	var out bytes.Buffer
	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	err := installServiceFlow("linux", home, "/usr/bin/veronica", "/cfg/config.yaml", false, false, run, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", "veronica.service"))
	if err != nil {
		t.Fatalf("expected unit written: %v", err)
	}
	if !strings.Contains(string(data), "ExecStart=/usr/bin/veronica serve --config /cfg/config.yaml") {
		t.Errorf("unexpected unit content:\n%s", data)
	}
	if len(calls) != 2 {
		t.Errorf("expected enable commands, got %v", calls)
	}
	if !strings.Contains(out.String(), "Installed and started") {
		t.Errorf("expected confirmation, got %q", out.String())
	}
}

func TestInstallServiceFlow_DryRunSkipsEnable(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	var out bytes.Buffer
	called := false
	run := func(name string, args ...string) error { called = true; return nil }

	err := installServiceFlow("linux", home, "/usr/bin/veronica", "/cfg/config.yaml", false, true, run, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("expected no enable commands in dry-run mode")
	}
	if !strings.Contains(out.String(), "ExecStart=") {
		t.Errorf("expected unit printed, got %q", out.String())
	}
}

func TestInstallServiceFlow_UninstallRemoves(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	target := filepath.Join(home, ".config", "systemd", "user", "veronica.service")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if err := os.WriteFile(target, []byte("unit"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	var out bytes.Buffer
	run := func(name string, args ...string) error { return nil }

	if err := installServiceFlow("linux", home, "/usr/bin/veronica", "/cfg/config.yaml", true, false, run, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("expected service file removed")
	}
}

func TestInstallServiceFlow_UnsupportedPlatform(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	run := func(name string, args ...string) error { return nil }

	if err := installServiceFlow("windows", t.TempDir(), "veronica", "", false, false, run, &out); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

func TestRunInstallService_DryRunPrintsUnit(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runInstallService("linux", "/cfg/config.yaml", false, true, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"serve --config /cfg/config.yaml", "WantedBy=default.target"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected output to contain %q, got %q", want, out.String())
		}
	}
}

func TestResolveExecPath_NonEmpty(t *testing.T) {
	t.Parallel()

	if resolveExecPath() == "" {
		t.Fatal("expected non-empty executable path")
	}
}
