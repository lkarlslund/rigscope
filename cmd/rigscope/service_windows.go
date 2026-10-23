//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName        = "rigscope"
	serviceDisplayName = "rigscope telemetry daemon"
	serviceDescription = "Continuous hardware telemetry collection with a browser UI."

	serviceSupported   = true
	serviceManagerName = "Windows Service Control Manager"
	// Services have no console, so serve writes its own log file.
	serviceLogsViaFlag = true

	serviceStateTimeout = 30 * time.Second
)

func defaultServiceConfig() serviceConfig {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return serviceConfig{
		addr:    "127.0.0.1:7077",
		binary:  filepath.Join(programFiles, "rigscope", "rigscope.exe"),
		dataDir: filepath.Join(programData, "rigscope", "data"),
		logFile: filepath.Join(programData, "rigscope", "logs", "rigscope.log"),
	}
}

func connectSCM() (*mgr.Mgr, error) {
	m, err := mgr.Connect()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, fmt.Errorf("managing services requires an elevated (Run as administrator) prompt")
	}
	return m, err
}

// openService connects to the SCM and opens the rigscope service. The caller
// must close both.
func openService() (*mgr.Mgr, *mgr.Service, error) {
	m, err := connectSCM()
	if err != nil {
		return nil, nil, err
	}
	s, err := m.OpenService(serviceName)
	if err != nil {
		_ = m.Disconnect()
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, nil, fmt.Errorf("service %q is not installed", serviceName)
		}
		return nil, nil, err
	}
	return m, s, nil
}

func waitServiceState(s *mgr.Service, want svc.State) error {
	deadline := time.Now().Add(serviceStateTimeout)
	for {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == want {
			return nil
		}
		if want == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf("service stopped during startup (exit code %d); see the log file", status.ServiceSpecificExitCode)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service state %s", stateName(want))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func stopService(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if status.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return err
		}
	}
	return waitServiceState(s, svc.Stopped)
}

func startService(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Running {
		return nil
	}
	if err := s.Start(); err != nil {
		return err
	}
	return waitServiceState(s, svc.Running)
}

func serviceInstall(out io.Writer, cfg serviceConfig, start bool) error {
	m, err := connectSCM()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()

	existing, err := m.OpenService(serviceName)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return err
	}
	if existing != nil {
		defer func() {
			_ = existing.Close()
		}()
		// Windows will not replace a running executable.
		if err := stopService(existing); err != nil {
			return fmt.Errorf("stopping existing service: %w", err)
		}
	}

	if err := installBinary(out, cfg.binary); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return err
	}

	config := mgr.Config{
		DisplayName:      serviceDisplayName,
		Description:      serviceDescription,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		ErrorControl:     mgr.ErrorNormal,
	}
	s := existing
	if s == nil {
		s, err = m.CreateService(serviceName, cfg.binary, config, cfg.serveArgs()...)
		if err != nil {
			return fmt.Errorf("creating service: %w", err)
		}
		defer func() {
			_ = s.Close()
		}()
	} else {
		current, err := s.Config()
		if err != nil {
			return fmt.Errorf("reading service config: %w", err)
		}
		config.ServiceType = current.ServiceType
		config.ServiceStartName = current.ServiceStartName
		config.BinaryPathName = windowsCommandLine(cfg.binary, cfg.serveArgs())
		if err := s.UpdateConfig(config); err != nil {
			return fmt.Errorf("updating service config: %w", err)
		}
	}

	// Mirror systemd's Restart=on-failure: restart after crashes and after
	// serve returning an error.
	restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: 3 * time.Second}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, uint32((24 * time.Hour).Seconds())); err != nil {
		return fmt.Errorf("setting recovery actions: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("enabling recovery on error exits: %w", err)
	}

	verb := "installed"
	if existing != nil {
		verb = "updated"
	}
	if _, err := fmt.Fprintf(out, "%s service %q\nlog: %s\n", verb, serviceName, cfg.logFile); err != nil {
		return err
	}
	if !start {
		return nil
	}
	if err := startService(s); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "started; listening on", httpEndpoint(cfg.addr))
	return err
}

func serviceUninstall(out io.Writer, cfg serviceConfig, removeData bool) error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()
	defer func() {
		_ = s.Close()
	}()

	if err := stopService(s); err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "removed service %q\n", serviceName); err != nil {
		return err
	}

	if err := removeInstalledBinary(out, cfg.binary); err != nil {
		return err
	}
	if removeData {
		return removeServiceData(out, cfg)
	}
	return nil
}

// removeInstalledBinary deletes the installed executable. When uninstall runs
// from that very file, Windows cannot delete it, so it is scheduled for
// deletion at the next reboot instead.
func removeInstalledBinary(out io.Writer, binary string) error {
	err := os.Remove(binary)
	switch {
	case err == nil:
		_ = os.Remove(filepath.Dir(binary))
		_, err = fmt.Fprintln(out, "removed binary:", binary)
		return err
	case os.IsNotExist(err):
		return nil
	}
	path, perr := windows.UTF16PtrFromString(binary)
	if perr != nil {
		return err
	}
	if merr := windows.MoveFileEx(path, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); merr != nil {
		return fmt.Errorf("removing %s: %w", binary, err)
	}
	_, err = fmt.Fprintln(out, "binary is in use; it will be deleted at next reboot:", binary)
	return err
}

func serviceStart(out io.Writer, _ serviceConfig) error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()
	defer func() {
		_ = s.Close()
	}()
	if err := startService(s); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "started", serviceName)
	return err
}

func serviceStop(out io.Writer) error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()
	defer func() {
		_ = s.Close()
	}()
	if err := stopService(s); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "stopped", serviceName)
	return err
}

func serviceRestart(out io.Writer, _ serviceConfig) error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()
	defer func() {
		_ = s.Close()
	}()
	if err := stopService(s); err != nil {
		return err
	}
	if err := startService(s); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "restarted", serviceName)
	return err
}

func serviceStatus(out io.Writer) error {
	// Querying needs no elevation, unlike mgr.Connect's default full access.
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	m := &mgr.Mgr{Handle: h}
	defer func() {
		_ = m.Disconnect()
	}()
	name, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return err
	}
	sh, err := windows.OpenService(h, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		_, err = fmt.Fprintln(out, "service: not installed")
		return err
	}
	if err != nil {
		return err
	}
	s := &mgr.Service{Name: serviceName, Handle: sh}
	defer func() {
		_ = s.Close()
	}()

	status, err := s.Query()
	if err != nil {
		return err
	}
	config, err := s.Config()
	if err != nil {
		return fmt.Errorf("reading service config: %w", err)
	}
	_, err = fmt.Fprintf(out, "service: %s\n  state: %s\n  pid: %d\n  start: %s\n  command: %s\n",
		serviceName, stateName(status.State), status.ProcessId, startTypeName(config), config.BinaryPathName)
	return err
}

func windowsCommandLine(binary string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, windows.EscapeArg(binary))
	for _, arg := range args {
		parts = append(parts, windows.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func stateName(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start pending"
	case svc.StopPending:
		return "stop pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue pending"
	case svc.PausePending:
		return "pause pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown (%d)", state)
	}
}

func startTypeName(config mgr.Config) string {
	switch config.StartType {
	case mgr.StartAutomatic:
		if config.DelayedAutoStart {
			return "automatic (delayed)"
		}
		return "automatic"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	default:
		return fmt.Sprintf("%d", config.StartType)
	}
}
