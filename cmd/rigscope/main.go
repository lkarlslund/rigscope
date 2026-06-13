package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/rigscope/internal/buildinfo"
	"github.com/lkarlslund/rigscope/internal/collectors"
	"github.com/lkarlslund/rigscope/internal/daemon"
	"github.com/lkarlslund/rigscope/internal/monitor"
	"github.com/lkarlslund/rigscope/internal/run"
	"github.com/lkarlslund/rigscope/internal/series"
	"github.com/lkarlslund/rigscope/internal/store"
	"github.com/lkarlslund/rigscope/internal/web"
)

func main() {
	if err := realMain(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "rigscope:", err)
		os.Exit(1)
	}
}

func realMain() error {
	return newRootCommand(os.Stdout, os.Stderr).Execute()
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	clientOpts := clientOptions{
		serverURL: "http://127.0.0.1:7077",
	}
	root := &cobra.Command{
		Use:           "rigscope",
		Short:         "Continuous hardware telemetry service",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&clientOpts.serverURL, "server", clientOpts.serverURL, "daemon HTTP server URL for client commands")

	serveOpts := serveOptions{
		addr:      "127.0.0.1:7077",
		dataDir:   "data",
		interval:  time.Second,
		retention: 0,
		logLevel:  "info",
	}
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Continuously collect telemetry and serve the web UI",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return serveCommand(serveOpts)
		},
	}
	serveCmd.Flags().StringVar(&serveOpts.addr, "addr", serveOpts.addr, "HTTP listen address")
	serveCmd.Flags().StringVar(&serveOpts.dataDir, "data-dir", serveOpts.dataDir, "directory for the embedded time-series database")
	serveCmd.Flags().DurationVar(&serveOpts.interval, "interval", serveOpts.interval, "telemetry sample interval")
	serveCmd.Flags().DurationVar(&serveOpts.retention, "retention", serveOpts.retention, "time-series retention period; 0 disables retention pruning")
	serveCmd.Flags().StringVar(&serveOpts.logLevel, "log-level", serveOpts.logLevel, "log level: debug, info, warn, error")
	serveCmd.Flags().BoolVar(&serveOpts.noNVIDIA, "no-nvidia", false, "disable nvidia-smi collector")
	serveCmd.Flags().BoolVar(&serveOpts.noROCM, "no-rocm", false, "disable rocm-smi collector")
	serveCmd.Flags().BoolVar(&serveOpts.noZenpower, "no-zenpower", false, "disable zenpower CPU package collector")

	runOpts := runOptions{
		name:       "run",
		outputRoot: "runs",
		interval:   500 * time.Millisecond,
	}
	runCmd := &cobra.Command{
		Use:   "run [options] -- <workload> [args...]",
		Short: "Legacy workload runner with stdout event capture",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("missing workload command")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return runCommand(runOpts, args)
		},
	}
	runCmd.Flags().StringVar(&runOpts.name, "name", runOpts.name, "run name")
	runCmd.Flags().StringVar(&runOpts.outputRoot, "output-root", runOpts.outputRoot, "directory for run outputs")
	runCmd.Flags().DurationVar(&runOpts.interval, "interval", runOpts.interval, "telemetry sample interval")
	runCmd.Flags().BoolVar(&runOpts.startPaused, "start-paused", false, "do not record until workload emits a recording/start event")
	runCmd.Flags().BoolVar(&runOpts.noEcho, "no-echo", false, "do not echo workload output")
	runCmd.Flags().BoolVar(&runOpts.noNVIDIA, "no-nvidia", false, "disable nvidia-smi collector")
	runCmd.Flags().BoolVar(&runOpts.noROCM, "no-rocm", false, "disable rocm-smi collector")
	runCmd.Flags().BoolVar(&runOpts.noZenpower, "no-zenpower", false, "disable zenpower CPU package collector")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := buildinfo.Current()
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s commit=%s built_at=%s\n", info.Version, info.Commit, info.BuiltAt)
			return err
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon build and metric status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statusCommand(cmd.Context(), cmd.OutOrStdout(), clientOpts)
		},
	}

	metricsCmd := &cobra.Command{
		Use:   "metrics",
		Short: "List metric series known by the daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return metricsCommand(cmd.Context(), cmd.OutOrStdout(), clientOpts)
		},
	}

	root.AddCommand(serveCmd, runCmd, versionCmd, statusCmd, metricsCmd)
	return root
}

type clientOptions struct {
	serverURL string
}

func (opts clientOptions) client() daemon.Client {
	return daemon.Client{BaseURL: opts.serverURL}
}

func statusCommand(ctx context.Context, out io.Writer, opts clientOptions) error {
	client := opts.client()
	info, err := client.Build(ctx)
	if err != nil {
		return err
	}
	metrics, err := client.Metrics(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "server: %s\nversion: %s\ncommit: %s\nbuilt_at: %s\nstarted_at: %s\npid: %d\nmetrics: %d\n",
		daemon.NormalizeBaseURLForDisplay(opts.serverURL),
		info.Version,
		info.Commit,
		info.BuiltAt,
		info.StartedAt,
		info.PID,
		len(metrics),
	)
	return err
}

func metricsCommand(ctx context.Context, out io.Writer, opts clientOptions) error {
	metrics, err := opts.client().Metrics(ctx)
	if err != nil {
		return err
	}
	for _, metric := range metrics {
		if _, err := fmt.Fprintln(out, formatMetric(metric)); err != nil {
			return err
		}
	}
	return nil
}

func formatMetric(metric series.Metric) string {
	parts := []string{metric.Name}
	if len(metric.Labels) > 0 {
		keys := make([]string, 0, len(metric.Labels))
		for key := range metric.Labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		labelParts := make([]string, 0, len(keys))
		for _, key := range keys {
			labelParts = append(labelParts, key+"="+metric.Labels[key])
		}
		parts = append(parts, "{"+strings.Join(labelParts, ",")+"}")
	}
	if metric.Unit != "" {
		parts = append(parts, "unit="+metric.Unit)
	}
	if metric.Symbol != "" {
		parts = append(parts, "symbol="+metric.Symbol)
	}
	if metric.Kind != "" {
		parts = append(parts, "kind="+metric.Kind)
	}
	return strings.Join(parts, " ")
}

type serveOptions struct {
	addr       string
	dataDir    string
	interval   time.Duration
	retention  time.Duration
	logLevel   string
	noNVIDIA   bool
	noROCM     bool
	noZenpower bool
}

func serveCommand(opts serveOptions) error {
	found, err := collectors.Default(collectors.DefaultOptions{
		NVIDIA:   !opts.noNVIDIA,
		ROCM:     !opts.noROCM,
		Zenpower: !opts.noZenpower,
	})
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("no collectors detected")
	}

	db, err := store.Open(filepath.Join(opts.dataDir, "tsdb"), opts.retention)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()

	signalCtx, stop := signalContext(context.Background())
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	level, err := parseLogLevel(opts.logLevel)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	webServer := &web.Server{
		Store:      db,
		LayoutPath: filepath.Join(opts.dataDir, "dashboard.json"),
	}
	mon := &monitor.Monitor{
		Collectors: found,
		Store:      db,
		Interval:   opts.interval,
		Log:        logger,
		OnSample:   webServer.PublishSample,
	}

	endpoint := httpEndpoint(opts.addr)
	fmt.Println("listening:", endpoint)
	fmt.Println("build info:", endpoint+"/api/build")
	fmt.Println("collectors:", collectorNames(found))
	fmt.Println("data:", filepath.Join(opts.dataDir, "tsdb"))

	return runServices(ctx,
		mon.Run,
		webServer.ListenAndServeContext(opts.addr),
	)
}

type serviceFunc func(context.Context) error

func runServices(ctx context.Context, services ...serviceFunc) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errc := make(chan error, len(services))
	for _, service := range services {
		go func() {
			errc <- service(ctx)
		}()
	}

	var result error
	for range services {
		err := <-errc
		cancel()
		if err == nil || errors.Is(err, context.Canceled) {
			continue
		}
		if result == nil {
			result = err
		}
	}
	if result != nil {
		return result
	}
	return nil
}

func httpEndpoint(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q", raw)
	}
}

type runOptions struct {
	name        string
	outputRoot  string
	interval    time.Duration
	startPaused bool
	noEcho      bool
	noNVIDIA    bool
	noROCM      bool
	noZenpower  bool
}

func runCommand(opts runOptions, workload []string) error {
	found, err := collectors.Default(collectors.DefaultOptions{
		NVIDIA:   !opts.noNVIDIA,
		ROCM:     !opts.noROCM,
		Zenpower: !opts.noZenpower,
	})
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("no collectors detected")
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	safeName := strings.NewReplacer("/", "-", " ", "-").Replace(opts.name)
	outputDir := filepath.Join(opts.outputRoot, stamp+"-"+safeName)

	ctx, stop := signalContext(context.Background())
	defer stop()

	cfg := run.Config{
		Name:               opts.name,
		Command:            workload,
		OutputDir:          outputDir,
		Interval:           opts.interval,
		Collectors:         found,
		RecordingInitially: !opts.startPaused,
		Echo:               !opts.noEcho,
	}

	fmt.Println("rigscope output:", outputDir)
	fmt.Println("collectors:", collectorNames(found))

	code, err := run.Workload(ctx, cfg)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func collectorNames(cs []collectors.Collector) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Name())
	}
	return strings.Join(names, ", ")
}
