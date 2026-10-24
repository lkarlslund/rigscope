package monitor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lkarlslund/rigscope/internal/collectors"
	"github.com/lkarlslund/rigscope/internal/series"
	"github.com/lkarlslund/rigscope/internal/store"
)

type fakeCollector struct {
	name string
	err  error
}

func (c fakeCollector) Name() string {
	return c.name
}

func (c fakeCollector) Sample(context.Context) (map[string]any, error) {
	if c.err != nil {
		return nil, c.err
	}
	return map[string]any{
		"collector":           c.name,
		"cpu_package_power_w": 42.5,
	}, nil
}

func TestRunSamplesUntilCanceled(t *testing.T) {
	s, err := store.OpenInMemory(time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mon := &Monitor{
		Collectors: []collectors.Collector{fakeCollector{name: "zenpower"}},
		Store:      s,
		Interval:   10 * time.Millisecond,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	errc := make(chan error, 1)
	go func() {
		errc <- mon.Run(ctx)
	}()

	metric := series.Metric{
		Name: "cpu_package_power_w",
		Labels: map[string]string{
			"collector": "zenpower",
		},
	}
	deadline := time.After(time.Second)
	for {
		points, err := s.Query(metric, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		if len(points) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("monitor did not insert a sample")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestCollectorErrorsReportsFailedCollectors(t *testing.T) {
	sample := map[string]any{
		"collectors": []map[string]any{
			{"collector": "nvidia"},
			{"collector": "xdna", "error": "sample timed out after 800ms"},
			{"error": "missing collector name"},
		},
	}

	got := collectorErrors(sample)
	if len(got) != 2 {
		t.Fatalf("len(errors) = %d, want 2: %#v", len(got), got)
	}
	if got[0]["collector"] != "xdna" || got[0]["error"] != "sample timed out after 800ms" {
		t.Fatalf("first error = %#v", got[0])
	}
	if got[1]["collector"] != "unknown" || got[1]["error"] != "missing collector name" {
		t.Fatalf("second error = %#v", got[1])
	}
}

func TestLogFailuresReportsTransitionsOnly(t *testing.T) {
	var buf bytes.Buffer
	mon := &Monitor{Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))}
	fail := []map[string]string{{"collector": "corsair_axi", "error": "usb write: connection timed out"}}
	mon.logFailures(fail)
	mon.logFailures(fail)
	mon.logFailures(nil)
	mon.logFailures(nil)

	want := `level=WARN msg="collector failing" collector=corsair_axi error="usb write: connection timed out"
level=INFO msg="collector recovered" collector=corsair_axi
`
	if buf.String() != want {
		t.Fatalf("log =\n%s\nwant\n%s", buf.String(), want)
	}
}
