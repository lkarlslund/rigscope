package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/rigscope/internal/series"
)

func TestWebsocketURL(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:7077":          "ws://127.0.0.1:7077/ws",
		"http://localhost:7077/":  "ws://localhost:7077/ws",
		"https://rig.example/api": "wss://rig.example/api/ws",
	}
	for input, want := range tests {
		got, err := websocketURL(input)
		if err != nil {
			t.Fatalf("websocketURL(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("websocketURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestKittyDashboardUsesTerminalNativeGraphs(t *testing.T) {
	m := newModel("http://localhost:7077", true, make(chan streamEvent))
	m.width, m.height = 180, 48
	points := []series.Point{
		{Metric: series.Metric{Name: "cpu_idle_pct", Kind: "cpu", Symbol: "%"}, Value: 90},
		{Metric: series.Metric{Name: "memory_used_bytes", Kind: "memory", Symbol: "B"}, Value: 8 << 30},
		{Metric: series.Metric{Name: "memory_total_bytes", Kind: "memory", Symbol: "B"}, Value: 32 << 30},
		{Metric: series.Metric{Name: "disk_written_bytes_per_second", Kind: "rate", Symbol: "B/s"}, Value: 4 << 20},
		{Metric: series.Metric{Name: "cpu_package_power_w", Kind: "power", Symbol: "W"}, Value: 45},
		{Metric: series.Metric{Name: "temperature_celsius", Kind: "temperature", Symbol: "°C", Labels: map[string]string{"sensor": "package"}}, Value: 58},
	}
	for _, p := range points {
		m.values[p.Key()] = p
		m.history[p.Key()] = []float64{p.Value * .9, p.Value}
	}
	view := m.overview(46)
	if strings.Contains(view, "\x1b_G") {
		t.Fatal("dashboard contains Kitty graphics commands that can corrupt Bubble Tea rendering")
	}
	if !strings.ContainsAny(view, "⠁⠂⠄⡀⢀⠈⠐⠠") {
		t.Fatal("dashboard contains no terminal-native graph")
	}
}

func TestOverviewScalesAllCardsIntoViewport(t *testing.T) {
	events := make(chan streamEvent)
	m := newModel("http://localhost:7077", false, events)
	m.width, m.height = 80, 24
	points := []series.Point{
		{Metric: series.Metric{Name: "cpu_idle_pct"}, Value: 70},
		{Metric: series.Metric{Name: "memory_used_bytes"}, Value: 4 << 30},
		{Metric: series.Metric{Name: "memory_total_bytes"}, Value: 16 << 30},
		{Metric: series.Metric{Name: "npu_present"}, Value: 1},
	}
	for i := 0; i < 3; i++ {
		points = append(points, series.Point{Metric: series.Metric{Name: "gpu_util_pct", Labels: map[string]string{"collector": "gpu", "index": string(rune('0' + i)), "device": "GPU"}}, Value: float64(i * 20)})
	}
	for _, point := range points {
		m.values[point.Key()] = point
		m.history[point.Key()] = []float64{point.Value}
	}
	body := m.overview(22)
	if got := lipgloss.Height(body); got != 22 {
		t.Fatalf("overview height = %d, want 22", got)
	}
	for _, title := range []string{"CPU", "SYSTEM MEMORY", "I/O THROUGHPUT", "POWER", "THERMALS", "NPU"} {
		if !strings.Contains(body, title) {
			t.Errorf("overview is missing %q", title)
		}
	}
}

func TestGraphRenderers(t *testing.T) {
	data := []graphSeries{{name: "package", values: []float64{42, 48, 45, 53}, color: graphPalette[0], unit: "°C"}}
	textGraph := brailleGraph(data, 80, 12)
	if !strings.Contains(textGraph, "⡀") && !strings.ContainsAny(textGraph, "⠁⠂⠄⡀⢀⠈⠐⠠") {
		t.Fatalf("text graph contains no braille plot: %q", textGraph)
	}
}

func TestModelIngestsSamplesAndHistory(t *testing.T) {
	events := make(chan streamEvent)
	m := newModel("http://localhost:7077", false, events)
	p := series.Point{Metric: series.Metric{Name: "cpu_idle_pct", Symbol: "%"}, Value: 70}
	updated, _ := m.Update(streamEvent{time: time.Now(), status: "local", data: sampleData{Points: []series.Point{p}}})
	got := updated.(model)
	if got.status != "local" || got.first("cpu_idle_pct") != 70 {
		t.Fatalf("sample not ingested: status=%q value=%v", got.status, got.first("cpu_idle_pct"))
	}
	if len(got.history[p.Key()]) != 1 {
		t.Fatalf("history length = %d, want 1", len(got.history[p.Key()]))
	}
}
