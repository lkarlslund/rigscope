package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/rigscope/internal/buildinfo"
	"github.com/lkarlslund/rigscope/internal/series"
	"github.com/lkarlslund/rigscope/internal/store"
)

func TestBuildEndpoint(t *testing.T) {
	oldVersion := buildinfo.Version
	oldCommit := buildinfo.Commit
	oldBuiltAt := buildinfo.BuiltAt
	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.Commit = oldCommit
		buildinfo.BuiltAt = oldBuiltAt
	})

	buildinfo.Version = "test-version"
	buildinfo.Commit = "test-commit"
	buildinfo.BuiltAt = "2026-06-12T00:00:00Z"

	req := httptest.NewRequest(http.MethodGet, "/api/build", nil)
	rec := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got buildinfo.Info
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Version != "test-version" {
		t.Fatalf("version = %q, want %q", got.Version, "test-version")
	}
	if got.Commit != "test-commit" {
		t.Fatalf("commit = %q, want %q", got.Commit, "test-commit")
	}
	if got.BuiltAt != "2026-06-12T00:00:00Z" {
		t.Fatalf("built_at = %q, want %q", got.BuiltAt, "2026-06-12T00:00:00Z")
	}
	if got.PID == 0 {
		t.Fatal("pid = 0, want process id")
	}
	if got.StartedAt == "" {
		t.Fatal("started_at is empty")
	}
}

func TestBuildEndpointIncludesAssetsHash(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/build", nil)
	rec := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(rec, req)

	var got map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["assets_hash"] == "" {
		t.Fatal("assets_hash is empty")
	}
	if got["api_version"].(float64) != 1 {
		t.Fatalf("api_version = %v, want 1", got["api_version"])
	}
}

func TestAssetsHashIsDeterministic(t *testing.T) {
	first := AssetsHash()
	second := AssetsHash()
	if first == "" {
		t.Fatal("AssetsHash() is empty")
	}
	if first != second {
		t.Fatalf("AssetsHash() = %q then %q", first, second)
	}
}

func TestLayoutPersistenceRoundTrip(t *testing.T) {
	srv := &Server{LayoutPath: filepath.Join(t.TempDir(), "dashboard.json")}
	layout := DashboardLayout{
		Version:   1,
		TimeRange: "1h",
		Order:     []string{"custom-1"},
		CustomGraphs: []Graph{{
			ID:    "custom-1",
			Title: "Power",
			Kind:  "custom",
			Series: []GraphSeries{{
				ID:     "s1",
				Metric: series.Metric{Name: "gpu_power_w", Unit: "watt", Symbol: "W"},
				Legend: "GPU",
				Color:  "#38bdf8",
			}},
		}},
	}
	if err := srv.SaveLayout(layout); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}
	got, err := srv.LoadLayout()
	if err != nil {
		t.Fatalf("LoadLayout: %v", err)
	}
	if got.TimeRange != "1h" {
		t.Fatalf("TimeRange = %q, want 1h", got.TimeRange)
	}
	if len(got.CustomGraphs) != 1 || got.CustomGraphs[0].ID != "custom-1" {
		t.Fatalf("custom graphs = %#v", got.CustomGraphs)
	}
}

func TestBatchQueryAppliesCounterRateTransform(t *testing.T) {
	db, err := store.OpenInMemory(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	metric := series.Metric{Name: "network_rx_bytes_total", Unit: "byte", Symbol: "B", Kind: "counter"}
	start := time.Unix(100, 0)
	if err := db.Insert(start, []series.Point{{Metric: metric, Value: 100}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(start.Add(2*time.Second), []series.Point{{Metric: metric, Value: 300}}); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(BatchQueryRequest{
		Start: start.Add(-time.Second).UnixMilli(),
		End:   start.Add(3 * time.Second).UnixMilli(),
		Series: []BatchSeriesQuery{{
			ID:        "net",
			Metric:    metric,
			Transform: "rate",
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/query/batch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	(&Server{Store: db}).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got BatchQueryResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Series) != 1 || len(got.Series[0].Points) != 1 {
		t.Fatalf("points = %#v", got.Series)
	}
	if got.Series[0].Points[0][1] != 100 {
		t.Fatalf("rate = %v, want 100", got.Series[0].Points[0][1])
	}
}

func TestDefaultPowerGraphIncludesWattMetricsAndExcludesLimits(t *testing.T) {
	graphs := DefaultGraphs([]series.Metric{
		{Name: "gpu_power_w", Unit: "watt", Symbol: "W", Kind: "power"},
		{Name: "cpu_package_power_w", Unit: "watt", Symbol: "W", Kind: "power"},
		{Name: "hwmon_power_w", Unit: "watt", Symbol: "W", Kind: "power"},
		{Name: "gpu_power_limit_w", Unit: "watt", Symbol: "W", Kind: "power_limit"},
	})
	if len(graphs) != 1 {
		t.Fatalf("graphs = %d, want 1", len(graphs))
	}
	if got := graphs[0].ID; got != "builtin-power" {
		t.Fatalf("graph ID = %q, want builtin-power", got)
	}
	if got := graphs[0].Title; got != "Power" {
		t.Fatalf("graph title = %q, want Power", got)
	}
	names := map[string]bool{}
	for _, item := range graphs[0].Series {
		names[item.Metric.Name] = true
	}
	for _, name := range []string{"gpu_power_w", "cpu_package_power_w", "hwmon_power_w"} {
		if !names[name] {
			t.Fatalf("Power graph missing %q: %#v", name, graphs[0].Series)
		}
	}
	if names["gpu_power_limit_w"] {
		t.Fatal("gpu_power_limit_w should not be in default Power graph")
	}
}

func TestDefaultPowerPrefersInProcessDRMOverROCM(t *testing.T) {
	graphs := DefaultGraphs([]series.Metric{
		{
			Name:   "gpu_power_w",
			Labels: map[string]string{"collector": "nvidia", "index": "0"},
			Unit:   "watt",
			Symbol: "W",
			Kind:   "power",
		},
		{
			Name:   "gpu_power_w",
			Labels: map[string]string{"collector": "rocm", "index": "0"},
			Unit:   "watt",
			Symbol: "W",
			Kind:   "power",
		},
		{
			Name:   "gpu_power_w",
			Labels: map[string]string{"collector": "drm", "card": "card1", "driver": "amdgpu"},
			Unit:   "watt",
			Symbol: "W",
			Kind:   "power",
		},
	})
	var power Graph
	for _, graph := range graphs {
		if graph.ID == "builtin-power" {
			power = graph
			break
		}
	}
	if len(power.Series) != 2 {
		t.Fatalf("Power series = %d, want 2: %#v", len(power.Series), power.Series)
	}
	for _, item := range power.Series {
		if item.Metric.Labels["collector"] == "rocm" {
			t.Fatal("ROCm GPU power should be hidden when in-process DRM GPU power is available")
		}
	}
}

func TestNormalizeLayoutMigratesOldPowerDefaults(t *testing.T) {
	layout := normalizeLayout(DashboardLayout{
		Order: []string{
			"builtin-cpu-power",
			"builtin-cpu-usage",
			"builtin-gpu-power",
			"builtin-gpu-util",
		},
		HiddenDefault: []string{"builtin-gpu-power"},
		CustomGraphs: []Graph{
			{
				ID:       "custom-old",
				Kind:     "custom",
				SourceID: "builtin-disk",
				Series: []GraphSeries{
					{
						Legend: "Gpu Power W NVIDIA RTX PRO 6000 Blackwell Workstation Edition 0",
						Metric: series.Metric{
							Name:   "gpu_power_w",
							Labels: map[string]string{"collector": "nvidia", "device": "NVIDIA RTX PRO 6000 Blackwell Workstation Edition", "index": "0"},
						},
					},
				},
			},
		},
	})
	wantOrder := []string{"builtin-power", "builtin-cpu-usage", "builtin-gpu-util"}
	if !slices.Equal(layout.Order, wantOrder) {
		t.Fatalf("order = %#v, want %#v", layout.Order, wantOrder)
	}
	if !slices.Equal(layout.HiddenDefault, []string{"builtin-power", "builtin-disk"}) {
		t.Fatalf("hidden = %#v, want builtin-power and builtin-disk", layout.HiddenDefault)
	}
	if got := layout.CustomGraphs[0].Series[0].Legend; got != "RTX 6000" {
		t.Fatalf("custom legend = %q, want RTX 6000", got)
	}
}

func TestNormalizeLayoutKeepsExplicitlyRestoredDefault(t *testing.T) {
	layout := normalizeLayout(DashboardLayout{
		Order:        []string{"custom-power", "builtin-power"},
		HiddenCustom: []string{"", "custom-hidden", "custom-hidden"},
		CustomGraphs: []Graph{
			{ID: "custom-power", Kind: "custom", SourceID: "builtin-power"},
		},
	})
	if len(layout.HiddenDefault) != 0 {
		t.Fatalf("hidden = %#v, want explicitly restored default visible", layout.HiddenDefault)
	}
	if !slices.Equal(layout.HiddenCustom, []string{"custom-hidden"}) {
		t.Fatalf("hidden custom = %#v, want deduplicated custom-hidden", layout.HiddenCustom)
	}
	if layout.CustomGraphs[0].ShowLegend == nil || !*layout.CustomGraphs[0].ShowLegend {
		t.Fatal("custom graph show_legend should default to true")
	}
}

func TestLabelSuffixShortensNoisyHardwareLabels(t *testing.T) {
	tests := []struct {
		name   string
		metric series.Metric
		want   string
	}{
		{
			name: "nvidia",
			metric: series.Metric{
				Name: "gpu_power_w",
				Labels: map[string]string{
					"collector": "nvidia",
					"device":    "NVIDIA RTX PRO 6000 Blackwell Workstation Edition",
					"index":     "0",
				},
			},
			want: " RTX 6000",
		},
		{
			name: "amd drm",
			metric: series.Metric{
				Name:   "gpu_power_w",
				Labels: map[string]string{"collector": "drm", "card": "card1", "chip": "amdgpu", "driver": "amdgpu"},
			},
			want: " AMD GPU",
		},
		{
			name:   "cpu package",
			metric: series.Metric{Name: "cpu_package_power_w", Labels: map[string]string{"collector": "zenpower"}},
			want:   " CPU package",
		},
	}
	for _, tt := range tests {
		if got := labelSuffix(tt.metric); got != tt.want {
			t.Fatalf("%s labelSuffix = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMetricLegendUsesCompactHardwareIdentity(t *testing.T) {
	tests := []struct {
		name   string
		metric series.Metric
		want   string
	}{
		{
			name: "nvidia gpu",
			metric: series.Metric{
				Name: "gpu_power_w",
				Labels: map[string]string{
					"collector": "nvidia",
					"device":    "NVIDIA RTX PRO 6000 Blackwell Workstation Edition",
					"index":     "0",
				},
			},
			want: "RTX 6000",
		},
		{
			name:   "amd gpu",
			metric: series.Metric{Name: "gpu_util_pct", Labels: map[string]string{"collector": "drm", "driver": "amdgpu"}},
			want:   "AMD GPU",
		},
		{
			name:   "cpu package",
			metric: series.Metric{Name: "cpu_package_power_w", Labels: map[string]string{"collector": "zenpower"}},
			want:   "CPU package",
		},
	}
	for _, tt := range tests {
		if got := metricLegend(tt.metric, ""); got != tt.want {
			t.Fatalf("%s metricLegend = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDefaultCounterRateGraphsDoNotExposeTotalLabels(t *testing.T) {
	graphs := DefaultGraphs([]series.Metric{
		{Name: "network_rx_bytes_per_second", Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
		{Name: "disk_read_bytes_per_second", Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
	})
	for _, graph := range graphs {
		if graph.Axes.Y.Symbol != "B/s" {
			t.Fatalf("%s y symbol = %q, want B/s", graph.ID, graph.Axes.Y.Symbol)
		}
		for _, item := range graph.Series {
			if item.Transform != "" {
				t.Fatalf("%s transform = %q, want collection-time rate metric", item.Metric.Name, item.Transform)
			}
			if item.Metric.Symbol != "B/s" {
				t.Fatalf("%s symbol = %q, want B/s", item.Metric.Name, item.Metric.Symbol)
			}
			if strings.Contains(item.Legend, "Total") {
				t.Fatalf("legend %q should not include Total for rate graph", item.Legend)
			}
			if !strings.Contains(item.Metric.Name, "_per_second") {
				t.Fatalf("metric %q should be a collection-time rate metric", item.Metric.Name)
			}
		}
	}
}

func TestDefaultDiskGraphExcludesPartitionDevices(t *testing.T) {
	graphs := DefaultGraphs([]series.Metric{
		{Name: "disk_read_bytes_per_second", Labels: map[string]string{"collector": "disk", "device": "nvme0n1"}, Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
		{Name: "disk_read_bytes_per_second", Labels: map[string]string{"collector": "disk", "device": "nvme0n1p1"}, Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
		{Name: "disk_written_bytes_per_second", Labels: map[string]string{"collector": "disk", "device": "sda"}, Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
		{Name: "disk_written_bytes_per_second", Labels: map[string]string{"collector": "disk", "device": "sda1"}, Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
		{Name: "disk_written_bytes_per_second", Labels: map[string]string{"collector": "disk", "device": "zram0"}, Unit: "byte/second", Symbol: "B/s", Kind: "rate"},
	})
	var disk Graph
	for _, graph := range graphs {
		if graph.ID == "builtin-disk" {
			disk = graph
			break
		}
	}
	if len(disk.Series) != 3 {
		t.Fatalf("disk series = %d, want whole devices only: %#v", len(disk.Series), disk.Series)
	}
	for _, item := range disk.Series {
		device := item.Metric.Labels["device"]
		if device == "nvme0n1p1" || device == "sda1" {
			t.Fatalf("partition device %q should not be in default disk graph", device)
		}
	}
}

func TestDefaultGraphsIncludeKulaStyleSystemPresets(t *testing.T) {
	graphs := DefaultGraphs([]series.Metric{
		{Name: "sockets_used", Unit: "count", Symbol: "count", Kind: "socket"},
		{Name: "tcp_connections_established", Unit: "count", Symbol: "count", Kind: "connection"},
		{Name: "filesystem_used_bytes", Labels: map[string]string{"collector": "filesystem", "mount": "/"}, Unit: "byte", Symbol: "B", Kind: "filesystem"},
		{Name: "filesystem_free_bytes", Labels: map[string]string{"collector": "filesystem", "mount": "/"}, Unit: "byte", Symbol: "B", Kind: "filesystem"},
		{Name: "power_supply_capacity_pct", Labels: map[string]string{"collector": "power_supply", "supply": "BAT0"}, Unit: "percent", Symbol: "%", Kind: "battery"},
	})

	seen := map[string]Graph{}
	for _, graph := range graphs {
		seen[graph.ID] = graph
	}
	for _, id := range []string{"builtin-connections", "builtin-disk-space", "builtin-battery"} {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing graph %q in %#v", id, graphs)
		}
	}
	if got := seen["builtin-connections"].Series[0].Legend; got != "Sockets" {
		t.Fatalf("connections legend = %q, want Sockets", got)
	}
	if got := seen["builtin-disk-space"].Axes.Y.Symbol; got != "B" {
		t.Fatalf("disk space symbol = %q, want B", got)
	}
	if got := seen["builtin-battery"].Axes.Y.Symbol; got != "%" {
		t.Fatalf("battery symbol = %q, want %%", got)
	}
}
