package store

import (
	"testing"
	"time"

	"github.com/lkarlslund/rigscope/internal/series"
)

func TestStoreInsertQuery(t *testing.T) {
	s, err := OpenInMemory(time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	metric := series.Metric{
		Name: "gpu_power_w",
		Labels: map[string]string{
			"collector": "nvidia",
			"index":     "0",
		},
		Unit: "W",
		Kind: "power",
	}
	ts := time.UnixMilli(1_700_000_000_000)
	if err := s.Insert(ts, []series.Point{{Metric: metric, Value: 450.5}}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	points, err := s.Query(metric, ts.Add(-time.Millisecond), ts.Add(time.Second))
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if got, want := len(points), 1; got != want {
		t.Fatalf("len(points) = %d, want %d", got, want)
	}
	if got, want := points[0].Value, 450.5; got != want {
		t.Fatalf("point value = %v, want %v", got, want)
	}

	metrics := s.Metrics()
	if got, want := len(metrics), 1; got != want {
		t.Fatalf("len(metrics) = %d, want %d", got, want)
	}
	metrics[0].Labels["index"] = "mutated"

	points, err = s.Query(metric, ts.Add(-time.Millisecond), ts.Add(time.Second))
	if err != nil {
		t.Fatalf("Query() after metrics mutation error = %v", err)
	}
	if got, want := len(points), 1; got != want {
		t.Fatalf("len(points) after metrics mutation = %d, want %d", got, want)
	}
}
