package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// maxLogFileSize is the size at which --log-file rotates to a single .1 backup on startup.
const maxLogFileSize = 10 << 20

// serviceConfig describes an installed rigscope system service. Paths default
// per platform (see defaultServiceConfig in the platform files).
type serviceConfig struct {
	binary    string
	dataDir   string
	logFile   string
	addr      string
	interval  time.Duration
	retention time.Duration
	logLevel  string
}

// serveArgs returns the arguments the service manager passes to the installed binary.
func (cfg serviceConfig) serveArgs() []string {
	args := []string{
		"serve",
		"--addr", cfg.addr,
		"--data-dir", cfg.dataDir,
		"--interval", cfg.interval.String(),
		"--retention", cfg.retention.String(),
		"--log-level", cfg.logLevel,
	}
	if cfg.logFile != "" && serviceLogsViaFlag {
		args = append(args, "--log-file", cfg.logFile)
	}
	return args
}

func newServiceCommand(clientOpts *clientOptions) *cobra.Command {
	cfg := defaultServiceConfig()
	cfg.interval = time.Second
	cfg.logLevel = "info"

	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install and manage rigscope as a system service",
		Long: "Install and manage rigscope as a system service (" + serviceManagerName + ").\n" +
			"Installing copies the running binary to --binary, so re-running install with a newer binary upgrades the service in place.",
	}
	cmd.PersistentFlags().StringVar(&cfg.binary, "binary", cfg.binary, "installed binary path")
	cmd.PersistentFlags().StringVar(&cfg.dataDir, "data-dir", cfg.dataDir, "service data directory")
	cmd.PersistentFlags().StringVar(&cfg.logFile, "log-file", cfg.logFile, "service log file")

	var noStart bool
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install or upgrade the service and start it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serviceInstall(cmd.OutOrStdout(), cfg, !noStart)
		},
	}
	installCmd.Flags().StringVar(&cfg.addr, "addr", cfg.addr, "HTTP listen address for the service")
	installCmd.Flags().DurationVar(&cfg.interval, "interval", cfg.interval, "telemetry sample interval")
	installCmd.Flags().DurationVar(&cfg.retention, "retention", cfg.retention, "time-series retention period; 0 disables retention pruning")
	installCmd.Flags().StringVar(&cfg.logLevel, "log-level", cfg.logLevel, "log level: debug, info, warn, error")
	installCmd.Flags().BoolVar(&noStart, "no-start", false, "install without starting the service now")

	var removeData bool
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the service and its binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serviceUninstall(cmd.OutOrStdout(), cfg, removeData)
		},
	}
	uninstallCmd.Flags().BoolVar(&removeData, "remove-data", false, "also delete the data directory and log file")

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serviceStart(cmd.OutOrStdout(), cfg)
		},
	}
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serviceStop(cmd.OutOrStdout())
		},
	}
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serviceRestart(cmd.OutOrStdout(), cfg)
		},
	}
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show service manager state and the running daemon's build",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if err := serviceStatus(out); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			info, err := clientOpts.client().Build(ctx)
			if err != nil {
				_, err = fmt.Fprintf(out, "daemon: unreachable at %s (%v)\n", clientOpts.serverURL, err)
				return err
			}
			_, err = fmt.Fprintf(out, "daemon: %s commit=%s pid=%d started_at=%s\n", info.Version, info.Commit, info.PID, info.StartedAt)
			return err
		},
	}

	cmd.AddCommand(installCmd, uninstallCmd, startCmd, stopCmd, restartCmd, statusCmd)
	return cmd
}

// openLogFile opens path for appending, first rotating it to path.1 when it
// has grown past maxLogFileSize.
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogFileSize {
		_ = os.Remove(path + ".1")
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
