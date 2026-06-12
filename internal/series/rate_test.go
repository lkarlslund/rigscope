package series

import (
	"testing"
	"time"
)

func TestCounterRateTransformerConvertsCountersBeforeStorage(t *testing.T) {
	var transformer CounterRateTransformer
	metric := Metric{
		Name:   "network_rx_bytes_total",
		Labels: map[string]string{"interface": "eth0"},
		Unit:   "byte",
		Symbol: "B",
		Kind:   "counter",
	}

	first := transformer.Transform(time.Unix(10, 0), []Point{{Metric: metric, Value: 1000}})
	if len(first) != 0 {
		t.Fatalf("first transform returned %d points, want 0", len(first))
	}

	second := transformer.Transform(time.Unix(12, 0), []Point{{Metric: metric, Value: 3000}})
	if len(second) != 1 {
		t.Fatalf("second transform returned %d points, want 1", len(second))
	}
	got := second[0]
	if got.Name != "network_rx_bytes_per_second" {
		t.Fatalf("name = %q, want network_rx_bytes_per_second", got.Name)
	}
	if got.Unit != "byte/second" {
		t.Fatalf("unit = %q, want byte/second", got.Unit)
	}
	if got.Symbol != "B/s" {
		t.Fatalf("symbol = %q, want B/s", got.Symbol)
	}
	if got.Kind != "rate" {
		t.Fatalf("kind = %q, want rate", got.Kind)
	}
	if got.Value != 1000 {
		t.Fatalf("value = %v, want 1000", got.Value)
	}
}

func TestCounterRateTransformerSkipsCounterReset(t *testing.T) {
	var transformer CounterRateTransformer
	metric := Metric{Name: "disk_reads_total", Unit: "count", Kind: "counter"}
	_ = transformer.Transform(time.Unix(10, 0), []Point{{Metric: metric, Value: 100}})
	got := transformer.Transform(time.Unix(11, 0), []Point{{Metric: metric, Value: 10}})
	if len(got) != 0 {
		t.Fatalf("reset transform returned %d points, want 0", len(got))
	}
}
