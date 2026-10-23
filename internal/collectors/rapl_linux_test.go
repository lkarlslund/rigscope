//go:build linux

package collectors

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRAPLZone(t *testing.T, root, id, name, energy string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "name"), name+"\n")
	writeFile(t, filepath.Join(dir, "energy_uj"), energy+"\n")
	writeFile(t, filepath.Join(dir, "max_energy_range_uj"), "262143328850\n")
}

func TestRAPLSamplesPowerFromEnergyDeltas(t *testing.T) {
	root := t.TempDir()
	writeRAPLZone(t, root, "intel-rapl:0", "package-0", "1000000")
	writeRAPLZone(t, root, "intel-rapl:0:0", "core", "500000")
	writeRAPLZone(t, root, "intel-rapl-mmio:0", "package-0", "1000000")

	c := &RAPL{Root: root}
	ok, err := c.detect()
	if err != nil || !ok {
		t.Fatalf("detect = %v, %v", ok, err)
	}
	if len(c.zones) != 2 {
		t.Fatalf("zones = %#v, want package and core without the mmio mirror", c.zones)
	}

	first, err := c.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(first["metrics"].([]map[string]any)); got != 0 {
		t.Fatalf("first sample has %d metrics, want none before a delta exists", got)
	}

	// Pretend the previous readings are one second old.
	for id, r := range c.last {
		r.at = r.at.Add(-time.Second)
		c.last[id] = r
	}
	writeFile(t, filepath.Join(root, "intel-rapl:0", "energy_uj"), "31000000\n")
	writeFile(t, filepath.Join(root, "intel-rapl:0:0", "energy_uj"), "20500000\n")

	sample, err := c.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, m := range sample["metrics"].([]map[string]any) {
		got[m["labels"].(map[string]string)["domain"]] = m
	}
	assertRAPLMetric(t, got["package-0"], "cpu_package_power_w", "power", "CPU package", 30)
	assertRAPLMetric(t, got["core"], "rapl_power_w", "power_component", "CPU cores", 20)
}

func assertRAPLMetric(t *testing.T, m map[string]any, name, kind, sensor string, watts float64) {
	t.Helper()
	if m == nil {
		t.Fatalf("missing %s metric", name)
	}
	if m["name"] != name || m["kind"] != kind || m["labels"].(map[string]string)["sensor"] != sensor {
		t.Fatalf("metric = %#v, want %s kind %s sensor %s", m, name, kind, sensor)
	}
	if v := m["value"].(float64); math.Abs(v-watts) > watts*0.01 {
		t.Fatalf("%s = %.3f W, want about %.0f W", name, v, watts)
	}
}

func TestRAPLWattsHandlesCounterWrap(t *testing.T) {
	now := time.Now()
	prev := raplReading{energyUJ: 999_000_000, at: now.Add(-time.Second)}
	cur := raplReading{energyUJ: 4_000_000, at: now}
	watts, ok := raplWatts(prev, cur, 1_000_000_000)
	if !ok || math.Abs(watts-5) > 0.001 {
		t.Fatalf("raplWatts = %.3f, %v; want 5 W across the wrap", watts, ok)
	}
	if _, ok := raplWatts(prev, cur, 0); ok {
		t.Fatal("a backwards counter without a known range must be dropped")
	}
}

func TestRAPLSkipsUnreadableZones(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "intel-rapl:0"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "intel-rapl:0", "name"), "package-0\n")
	ok, err := (&RAPL{Root: root}).detect()
	if err != nil || ok {
		t.Fatalf("detect = %v, %v; want no collector without a readable energy_uj", ok, err)
	}
}

func TestPreferLowOverheadCollectorsSkipsRAPLWithZenpower(t *testing.T) {
	got := preferLowOverheadCollectors([]Collector{Zenpower{}, &RAPL{}})
	if len(got) != 1 || got[0].Name() != "zenpower" {
		t.Fatalf("collectors = %#v, want only zenpower", got)
	}
}
