//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const (
	serviceSupported   = true
	serviceManagerName = "launchd"
	// launchd captures stdout/stderr itself, so serve needs no --log-file.
	serviceLogsViaFlag = false

	launchdPlistPath = "/Library/LaunchDaemons/" + launchdLabel + ".plist"
	launchdTarget    = "system/" + launchdLabel
)

func defaultServiceConfig() serviceConfig {
	return serviceConfig{
		addr:    "127.0.0.1:7077",
		binary:  "/usr/local/bin/rigscope",
		dataDir: "/Library/Application Support/rigscope/data",
		logFile: "/Library/Logs/rigscope/rigscope.log",
	}
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("managing the launchd daemon requires root; re-run with sudo")
	}
	return nil
}

func launchctl(args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command("launchctl", args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func launchdLoaded() bool {
	_, err := launchctl("print", launchdTarget)
	return err == nil
}

func serviceInstall(out io.Writer, cfg serviceConfig, start bool) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if err := installBinary(out, cfg.binary); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(launchdPlistPath, launchdPlist(cfg), 0o644); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "launchd plist:", launchdPlistPath); err != nil {
		return err
	}

	// Reload so launchd picks up a changed plist and the new binary.
	if launchdLoaded() {
		if _, err := launchctl("bootout", launchdTarget); err != nil {
			return err
		}
	}
	if !start {
		_, err := fmt.Fprintln(out, "installed; it starts at next boot or with 'rigscope service start'")
		return err
	}
	if _, err := launchctl("bootstrap", "system", launchdPlistPath); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "started", launchdTarget, "listening on", httpEndpoint(cfg.addr))
	return err
}

func serviceUninstall(out io.Writer, cfg serviceConfig, removeData bool) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if launchdLoaded() {
		if _, err := launchctl("bootout", launchdTarget); err != nil {
			return err
		}
	}
	for _, path := range []string{launchdPlistPath, cfg.binary} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := fmt.Fprintln(out, "removed", launchdTarget); err != nil {
		return err
	}
	if removeData {
		return removeServiceData(out, cfg)
	}
	return nil
}

func serviceStart(out io.Writer, _ serviceConfig) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if _, err := os.Stat(launchdPlistPath); err != nil {
		return fmt.Errorf("service not installed: %w", err)
	}
	var err error
	if launchdLoaded() {
		_, err = launchctl("kickstart", launchdTarget)
	} else {
		_, err = launchctl("bootstrap", "system", launchdPlistPath)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "started", launchdTarget)
	return err
}

func serviceStop(out io.Writer) error {
	if err := requireRoot(); err != nil {
		return err
	}
	// A clean SIGTERM exit keeps launchd from restarting it (KeepAlive only
	// covers unsuccessful exits), while leaving the job loaded for boot.
	if _, err := launchctl("kill", "SIGTERM", launchdTarget); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "stopped", launchdTarget)
	return err
}

func serviceRestart(out io.Writer, cfg serviceConfig) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if !launchdLoaded() {
		return serviceStart(out, cfg)
	}
	if _, err := launchctl("kickstart", "-k", launchdTarget); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "restarted", launchdTarget)
	return err
}

func serviceStatus(out io.Writer) error {
	if _, err := os.Stat(launchdPlistPath); err != nil {
		_, err = fmt.Fprintln(out, "service: not installed")
		return err
	}
	text, err := launchctl("print", launchdTarget)
	if err != nil {
		_, err = fmt.Fprintln(out, "service: installed, not loaded")
		return err
	}
	if _, err := fmt.Fprintln(out, "service:", launchdTarget); err != nil {
		return err
	}
	// Keep only the useful top-level fields from launchctl's verbose dump;
	// nested sections repeat some keys, so print the first of each.
	seen := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"state = ", "pid = ", "last exit code = ", "program = "} {
			if strings.HasPrefix(line, prefix) && !seen[prefix] {
				seen[prefix] = true
				if _, err := fmt.Fprintln(out, "  "+line); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
