//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	serviceSupported   = true
	serviceManagerName = "systemd"
	// journald captures stdout/stderr, so serve needs no --log-file.
	serviceLogsViaFlag = false

	systemdUnitName = "rigscope.service"
	systemdUnitPath = "/etc/systemd/system/" + systemdUnitName
	udevRulesPath   = "/etc/udev/rules.d/70-rigscope.rules"
)

func defaultServiceConfig() serviceConfig {
	return serviceConfig{
		// Existing installs listen on all interfaces; keep that so re-running
		// install does not silently make the dashboard loopback-only.
		addr:    "0.0.0.0:7077",
		binary:  "/opt/rigscope/rigscope",
		dataDir: "/opt/rigscope/data",
	}
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("managing the systemd service requires root; re-run with sudo")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl not found; the service command needs systemd")
	}
	return nil
}

func runTool(name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func systemctl(args ...string) error {
	_, err := runTool("systemctl", args...)
	return err
}

// ensureServiceUser creates the rigscope system group and user when missing
// and returns their ids.
func ensureServiceUser(home string) (uid, gid int, err error) {
	if _, err := user.LookupGroup(systemdGroup); err != nil {
		if _, err := runTool("groupadd", "--system", systemdGroup); err != nil {
			return 0, 0, err
		}
	}
	if _, err := user.Lookup(systemdUser); err != nil {
		if _, err := runTool("useradd", "--system", "--gid", systemdGroup, "--home-dir", home, "--no-create-home", "--shell", "/usr/sbin/nologin", systemdUser); err != nil {
			return 0, 0, err
		}
	}
	u, err := user.Lookup(systemdUser)
	if err != nil {
		return 0, 0, err
	}
	g, err := user.LookupGroup(systemdGroup)
	if err != nil {
		return 0, 0, err
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, err
	}
	if gid, err = strconv.Atoi(g.Gid); err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func serviceInstall(out io.Writer, cfg serviceConfig, start bool) error {
	if err := requireRoot(); err != nil {
		return err
	}
	uid, gid, err := ensureServiceUser(filepath.Dir(cfg.binary))
	if err != nil {
		return err
	}
	// Replacing the binary by rename is safe while the old one still runs.
	if err := installBinary(out, cfg.binary); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return err
	}
	if err := os.Chown(cfg.dataDir, uid, gid); err != nil {
		return err
	}
	if err := installUdevRules(out); err != nil {
		return err
	}
	if err := os.WriteFile(systemdUnitPath, []byte(systemdUnit(cfg)), 0o644); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "unit:", systemdUnitPath); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", systemdUnitName); err != nil {
		return err
	}
	if !start {
		_, err := fmt.Fprintln(out, "installed and enabled; it starts at next boot or with 'rigscope service start'")
		return err
	}
	// restart also starts a stopped unit, and picks up a new binary or unit.
	if err := systemctl("restart", systemdUnitName); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "started", systemdUnitName, "listening on", httpEndpoint(cfg.addr))
	return err
}

// installUdevRules writes the hardware access rules and applies them to
// devices that are already present.
func installUdevRules(out io.Writer) error {
	if err := os.WriteFile(udevRulesPath, []byte(udevRules()), 0o644); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "udev rules:", udevRulesPath); err != nil {
		return err
	}
	reloadUdev(out)
	return nil
}

// reloadUdev is best effort: without udev the service still runs, only
// without access to the power sensors.
func reloadUdev(out io.Writer) {
	for _, args := range [][]string{
		{"control", "--reload"},
		{"trigger", "--action=add", "--subsystem-match=powercap"},
		{"trigger", "--action=add", "--subsystem-match=usb", "--attr-match=idVendor=1b1c"},
		{"settle"},
	} {
		if _, err := runTool("udevadm", args...); err != nil {
			_, _ = fmt.Fprintln(out, "warning:", err)
			return
		}
	}
}

func serviceUninstall(out io.Writer, cfg serviceConfig, removeData bool) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if _, err := os.Stat(systemdUnitPath); err == nil {
		if err := systemctl("disable", "--now", systemdUnitName); err != nil {
			return err
		}
	}
	for _, path := range []string{systemdUnitPath, udevRulesPath, cfg.binary} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	reloadUdev(out)
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "removed", systemdUnitName); err != nil {
		return err
	}
	if !removeData {
		_, err := fmt.Fprintln(out, "left data in place:", cfg.dataDir)
		return err
	}
	if err := removeServiceData(out, cfg); err != nil {
		return err
	}
	// Drop the install directory once nothing else lives there.
	_ = os.Remove(filepath.Dir(cfg.binary))
	if _, err := user.Lookup(systemdUser); err == nil {
		if _, err := runTool("userdel", systemdUser); err != nil {
			return err
		}
	}
	if _, err := user.LookupGroup(systemdGroup); err == nil {
		if _, err := runTool("groupdel", systemdGroup); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "removed user and group:", systemdUser)
	return err
}

func serviceStart(out io.Writer, _ serviceConfig) error {
	return systemctlVerb(out, "start", "started")
}

func serviceStop(out io.Writer) error {
	return systemctlVerb(out, "stop", "stopped")
}

func serviceRestart(out io.Writer, _ serviceConfig) error {
	return systemctlVerb(out, "restart", "restarted")
}

func systemctlVerb(out io.Writer, verb, done string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if _, err := os.Stat(systemdUnitPath); err != nil {
		return fmt.Errorf("service not installed: %w", err)
	}
	if err := systemctl(verb, systemdUnitName); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, done, systemdUnitName)
	return err
}

func serviceStatus(out io.Writer) error {
	if _, err := os.Stat(systemdUnitPath); err != nil {
		_, err = fmt.Fprintln(out, "service: not installed")
		return err
	}
	text, err := runTool("systemctl", "show", systemdUnitName,
		"--property=ActiveState,SubState,MainPID,UnitFileState,ActiveEnterTimestamp")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "service:", systemdUnitName); err != nil {
		return err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		key, value, _ := strings.Cut(line, "=")
		if _, err := fmt.Fprintf(out, "  %s: %s\n", key, value); err != nil {
			return err
		}
	}
	return nil
}
