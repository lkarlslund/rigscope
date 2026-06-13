package collectors

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegisteredCollectorsIncludesCoreSubsystems(t *testing.T) {
	seen := map[string]bool{}
	for _, reg := range Registered() {
		seen[reg.Name] = true
	}
	names := []string{"cpu", "disk", "filesystem", "memory", "network", "nvidia", "process", "rocm", "self"}
	if runtime.GOOS == "linux" {
		names = append(names, "drm", "load", "power_supply", "socket", "thermal", "xdna", "zenpower")
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("collector %q not registered", name)
		}
	}
}

func TestMetricDefaultSymbols(t *testing.T) {
	tests := []struct {
		unit string
		want string
	}{
		{unit: "count", want: "count"},
		{unit: "ratio", want: "1"},
		{unit: "second", want: "s"},
		{unit: "byte", want: ""},
	}
	for _, tt := range tests {
		if got := defaultSymbol(tt.unit); got != tt.want {
			t.Fatalf("defaultSymbol(%q) = %q, want %q", tt.unit, got, tt.want)
		}
	}
}

func TestSamplerTimesOutOneCollectorWithoutBlockingOthers(t *testing.T) {
	blocked := &blockingCollector{name: "blocked", started: make(chan struct{}), release: make(chan struct{})}
	fast := staticCollector{name: "fast"}
	sampler := Sampler{Timeout: 20 * time.Millisecond, StaleAfter: time.Hour}

	sample := sampler.SampleAll(context.Background(), []Collector{blocked, fast})
	records := sample["collectors"].([]map[string]any)
	if got, want := len(records), 2; got != want {
		t.Fatalf("len(records) = %d, want %d", got, want)
	}
	if records[0]["collector"] != "blocked" || !strings.Contains(records[0]["error"].(string), "timed out") {
		t.Fatalf("blocked record = %#v, want timeout error", records[0])
	}
	if records[1]["collector"] != "fast" || records[1]["ok"] != true {
		t.Fatalf("fast record = %#v, want successful record", records[1])
	}

	select {
	case <-blocked.started:
	default:
		t.Fatal("blocked collector was not started")
	}
	if got := blocked.calls.Load(); got != 1 {
		t.Fatalf("blocked calls = %d, want 1", got)
	}

	sample = sampler.SampleAll(context.Background(), []Collector{blocked, fast})
	records = sample["collectors"].([]map[string]any)
	if records[0]["collector"] != "blocked" || records[0]["error"] != "previous sample still running" {
		t.Fatalf("blocked second record = %#v, want in-flight error", records[0])
	}
	if got := blocked.calls.Load(); got != 1 {
		t.Fatalf("blocked calls after second sample = %d, want 1", got)
	}

	close(blocked.release)
}

func TestSamplerRetriesStaleInFlightCollector(t *testing.T) {
	blocked := &blockingCollector{name: "blocked", started: make(chan struct{}), release: make(chan struct{})}
	sampler := Sampler{Timeout: 10 * time.Millisecond, StaleAfter: 15 * time.Millisecond}

	_ = sampler.SampleAll(context.Background(), []Collector{blocked})
	time.Sleep(20 * time.Millisecond)
	sample := sampler.SampleAll(context.Background(), []Collector{staticCollector{name: "blocked"}})
	records := sample["collectors"].([]map[string]any)
	if records[0]["collector"] != "blocked" || records[0]["ok"] != true {
		t.Fatalf("stale retry record = %#v, want successful replacement sample", records[0])
	}

	close(blocked.release)
}

type staticCollector struct {
	name string
}

func (c staticCollector) Name() string { return c.name }

func (c staticCollector) Sample(context.Context) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

type blockingCollector struct {
	name    string
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (c *blockingCollector) Name() string { return c.name }

func (c *blockingCollector) Sample(context.Context) (map[string]any, error) {
	c.calls.Add(1)
	c.once.Do(func() { close(c.started) })
	<-c.release
	return map[string]any{"ok": true}, nil
}
