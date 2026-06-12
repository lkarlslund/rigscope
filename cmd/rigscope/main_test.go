package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/rigscope/internal/buildinfo"
	"github.com/lkarlslund/rigscope/internal/daemon"
	"github.com/lkarlslund/rigscope/internal/series"
)

func TestRootHelpUsesCobraCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(&stdout, &stderr)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"serve", "run", "version", "status", "metrics"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRootWithoutSubcommandShowsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(&stdout, &stderr)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Available Commands") {
		t.Fatalf("stdout = %q, want help output", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(&stdout, &stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "commit=") || !strings.Contains(got, "built_at=") {
		t.Fatalf("version output = %q, want commit and built_at", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunCommandRequiresWorkload(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(&stdout, &stderr)
	cmd.SetArgs([]string{"run"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing workload error")
	}
	if !strings.Contains(err.Error(), "missing workload command") {
		t.Fatalf("Execute() error = %v, want missing workload command", err)
	}
}

func TestStatusCommandQueriesDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/build":
			_ = json.NewEncoder(w).Encode(buildinfo.Info{
				Version:   "daemon-version",
				Commit:    "abc123",
				BuiltAt:   "2026-06-12T00:00:00Z",
				StartedAt: "2026-06-12T00:01:00Z",
				PID:       42,
			})
		case "/api/metrics":
			_ = json.NewEncoder(w).Encode(daemon.MetricsResponse{
				Metrics: []series.Metric{{Name: "gpu_power_w"}, {Name: "cpu_package_power_w"}},
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(&stdout, &stderr)
	cmd.SetArgs([]string{"--server", srv.URL, "status"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"server: " + srv.URL, "version: daemon-version", "pid: 42", "metrics: 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestMetricsCommandQueriesDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics" {
			t.Fatalf("path = %q, want /api/metrics", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(daemon.MetricsResponse{
			Metrics: []series.Metric{
				{
					Name: "gpu_power_w",
					Labels: map[string]string{
						"index":     "0",
						"collector": "nvidia",
					},
					Unit:   "W",
					Symbol: "W",
					Kind:   "power",
				},
			},
		})
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(&stdout, &stderr)
	cmd.SetArgs([]string{"--server", srv.URL, "metrics"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "gpu_power_w {collector=nvidia,index=0} unit=W symbol=W kind=power\n"
	if got := stdout.String(); got != want {
		t.Fatalf("metrics output = %q, want %q", got, want)
	}
}

func TestRunServicesWaitsForGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})

	errc := make(chan error, 1)
	go func() {
		errc <- runServices(ctx,
			func(ctx context.Context) error {
				cancel()
				<-ctx.Done()
				close(firstDone)
				return ctx.Err()
			},
			func(ctx context.Context) error {
				<-ctx.Done()
				time.Sleep(20 * time.Millisecond)
				close(secondDone)
				return ctx.Err()
			},
		)
	}()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("runServices() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runServices() did not return")
	}

	select {
	case <-firstDone:
	default:
		t.Fatal("first service was not allowed to finish")
	}
	select {
	case <-secondDone:
	default:
		t.Fatal("second service was not allowed to finish")
	}
}

func TestRunServicesCancelsPeersAfterError(t *testing.T) {
	want := errors.New("listener failed")
	peerCanceled := make(chan struct{})

	err := runServices(context.Background(),
		func(context.Context) error {
			return want
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			close(peerCanceled)
			return ctx.Err()
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("runServices() error = %v, want %v", err, want)
	}

	select {
	case <-peerCanceled:
	default:
		t.Fatal("peer service was not canceled after error")
	}
}

func TestHTTPEndpoint(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "loopback", addr: "127.0.0.1:7077", want: "http://127.0.0.1:7077"},
		{name: "wildcard", addr: "0.0.0.0:7077", want: "http://127.0.0.1:7077"},
		{name: "bare port", addr: ":7077", want: "http://127.0.0.1:7077"},
		{name: "ipv6 wildcard", addr: "[::]:7077", want: "http://127.0.0.1:7077"},
		{name: "qualified url", addr: "http://localhost:7077/", want: "http://localhost:7077"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpEndpoint(tt.addr); got != tt.want {
				t.Fatalf("httpEndpoint(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		raw  string
		want slog.Level
	}{
		{raw: "debug", want: slog.LevelDebug},
		{raw: "INFO", want: slog.LevelInfo},
		{raw: "warning", want: slog.LevelWarn},
		{raw: "error", want: slog.LevelError},
	}

	for _, tt := range tests {
		got, err := parseLogLevel(tt.raw)
		if err != nil {
			t.Fatalf("parseLogLevel(%q) error = %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}

	if _, err := parseLogLevel("trace"); err == nil {
		t.Fatal("parseLogLevel(\"trace\") error = nil, want error")
	}
}
