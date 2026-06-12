package web

import (
	"slices"
	"strings"

	"github.com/lkarlslund/rigscope/internal/series"
)

type DashboardLayout struct {
	Version       int      `json:"version"`
	TimeRange     string   `json:"time_range"`
	Order         []string `json:"order"`
	HiddenDefault []string `json:"hidden_default,omitempty"`
	HiddenCustom  []string `json:"hidden_custom,omitempty"`
	CustomGraphs  []Graph  `json:"custom_graphs,omitempty"`
}

type Graph struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Kind       string        `json:"kind"`
	SourceID   string        `json:"source_id,omitempty"`
	Size       string        `json:"size,omitempty"`
	Stacked    bool          `json:"stacked,omitempty"`
	ShowLegend *bool         `json:"show_legend,omitempty"`
	Series     []GraphSeries `json:"series"`
	Axes       GraphAxes     `json:"axes"`
}

type GraphSeries struct {
	ID        string            `json:"id"`
	Metric    series.Metric     `json:"metric"`
	Legend    string            `json:"legend"`
	Color     string            `json:"color"`
	Axis      string            `json:"axis,omitempty"`
	Transform string            `json:"transform,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type GraphAxes struct {
	X  Axis `json:"x"`
	Y  Axis `json:"y"`
	Y2 Axis `json:"y2,omitempty"`
}

type Axis struct {
	Label     string  `json:"label,omitempty"`
	Unit      string  `json:"unit,omitempty"`
	Symbol    string  `json:"symbol,omitempty"`
	Mode      string  `json:"mode,omitempty"`
	Min       float64 `json:"min,omitempty"`
	Max       float64 `json:"max,omitempty"`
	BeginZero bool    `json:"begin_zero,omitempty"`
}

type BatchQueryRequest struct {
	Start     int64              `json:"start"`
	End       int64              `json:"end"`
	MaxPoints int                `json:"max_points"`
	Series    []BatchSeriesQuery `json:"series"`
}

type BatchSeriesQuery struct {
	ID        string        `json:"id"`
	Metric    series.Metric `json:"metric"`
	Transform string        `json:"transform,omitempty"`
}

type BatchQueryResponse struct {
	Start  int64                 `json:"start"`
	End    int64                 `json:"end"`
	Series []BatchSeriesResponse `json:"series"`
}

type BatchSeriesResponse struct {
	ID     string        `json:"id"`
	Metric series.Metric `json:"metric"`
	Points [][2]float64  `json:"points"`
}

func DefaultLayout() DashboardLayout {
	return DashboardLayout{
		Version:   1,
		TimeRange: "15m",
		Order: []string{
			"builtin-power",
			"builtin-cpu-usage",
			"builtin-gpu-util",
			"builtin-gpu-memory",
			"builtin-npu-runtime",
			"builtin-thermals",
			"builtin-battery",
			"builtin-memory",
			"builtin-network",
			"builtin-connections",
			"builtin-disk",
			"builtin-disk-space",
			"builtin-processes",
		},
	}
}

func normalizeLayout(layout DashboardLayout) DashboardLayout {
	if layout.Version == 0 {
		layout.Version = 1
	}
	if layout.TimeRange == "" {
		layout.TimeRange = "15m"
	}
	layout.Order = migrateDefaultPowerGraph(layout.Order)
	layout.HiddenDefault = migrateDefaultPowerGraph(layout.HiddenDefault)
	layout.HiddenDefault = hideCustomizedDefaults(layout.HiddenDefault, layout.CustomGraphs, layout.Order)
	layout.HiddenCustom = normalizeIDList(layout.HiddenCustom)
	for i := range layout.CustomGraphs {
		if layout.CustomGraphs[i].Kind == "" {
			layout.CustomGraphs[i].Kind = "custom"
		}
		if layout.CustomGraphs[i].Size == "" {
			layout.CustomGraphs[i].Size = "normal"
		}
		if layout.CustomGraphs[i].ShowLegend == nil {
			showLegend := true
			layout.CustomGraphs[i].ShowLegend = &showLegend
		}
		for j := range layout.CustomGraphs[i].Series {
			item := &layout.CustomGraphs[i].Series[j]
			item.Legend = metricLegend(item.Metric, item.Transform)
		}
	}
	return layout
}

func normalizeIDList(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		out = append(out, id)
		seen[id] = true
	}
	return out
}

func hideCustomizedDefaults(hidden []string, custom []Graph, order []string) []string {
	seen := map[string]bool{}
	explicitOrder := map[string]bool{}
	for _, id := range order {
		explicitOrder[id] = true
	}
	out := make([]string, 0, len(hidden)+len(custom))
	for _, id := range hidden {
		if id == "" || seen[id] {
			continue
		}
		out = append(out, id)
		seen[id] = true
	}
	for _, graph := range custom {
		if explicitOrder[graph.SourceID] {
			continue
		}
		if graph.SourceID == "" || seen[graph.SourceID] {
			continue
		}
		out = append(out, graph.SourceID)
		seen[graph.SourceID] = true
	}
	return out
}

func migrateDefaultPowerGraph(ids []string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, 0, len(ids))
	hasPower := false
	for _, id := range ids {
		switch id {
		case "builtin-cpu-power", "builtin-gpu-power", "builtin-power":
			if hasPower {
				continue
			}
			out = append(out, "builtin-power")
			hasPower = true
		default:
			out = append(out, id)
		}
	}
	return out
}

func DefaultGraphs(metrics []series.Metric) []Graph {
	var graphs []Graph
	add := func(id, title, kind string, names ...string) {
		seriesList := graphSeries(metrics, names...)
		if len(seriesList) == 0 {
			return
		}
		unit, symbol := commonUnit(seriesList)
		graphs = append(graphs, Graph{
			ID:     id,
			Title:  title,
			Kind:   "builtin",
			Size:   "normal",
			Series: seriesList,
			Axes: GraphAxes{
				X: Axis{Label: "Time", Mode: "time"},
				Y: Axis{Label: kind, Unit: unit, Symbol: symbol, Mode: "auto", BeginZero: beginZero(kind)},
			},
		})
	}
	if seriesList := graphSeriesByPredicate(metrics, isPowerMetric); len(seriesList) > 0 {
		graphs = append(graphs, Graph{
			ID:      "builtin-power",
			Title:   "Power",
			Kind:    "builtin",
			Size:    "normal",
			Stacked: false,
			Series:  seriesList,
			Axes: GraphAxes{
				X: Axis{Label: "Time", Mode: "time"},
				Y: Axis{Label: "Power", Unit: "watt", Symbol: "W", Mode: "auto", BeginZero: true},
			},
		})
	}
	add("builtin-cpu-usage", "CPU Usage", "Utilization", "cpu_user_pct", "cpu_system_pct", "cpu_iowait_pct", "cpu_irq_pct")
	add("builtin-gpu-util", "GPU Load", "Utilization", "gpu_util_pct")
	add("builtin-gpu-memory", "GPU Memory", "Memory", "gpu_mem_used_mib", "gpu_vram_used_bytes", "gpu_gtt_used_bytes")
	add("builtin-npu-runtime", "NPU Runtime", "Activity", "npu_runtime_active", "npu_runtime_active_seconds_per_second", "npu_runtime_suspended_seconds_per_second")
	add("builtin-thermals", "Thermals", "Temperature", "temperature_celsius", "gpu_temp_c", "gpu_temp_celsius", "power_supply_temp_celsius")
	add("builtin-battery", "Battery", "Battery", "power_supply_capacity_pct")
	add("builtin-memory", "Memory", "Memory", "memory_used_bytes", "memory_available_bytes", "swap_used_bytes")
	add("builtin-network", "Network Throughput", "Throughput", "network_rx_bytes_per_second", "network_tx_bytes_per_second")
	add(
		"builtin-connections",
		"Connections & Sockets",
		"Connections",
		"sockets_used",
		"tcp_sockets_in_use",
		"tcp_sockets_time_wait",
		"udp_sockets_in_use",
		"tcp_connections_established",
		"tcp_connections_listen",
		"tcp_connections_time_wait",
	)
	if seriesList := graphSeriesByPredicate(metrics, isDefaultDiskThroughputMetric); len(seriesList) > 0 {
		unit, symbol := commonUnit(seriesList)
		graphs = append(graphs, Graph{
			ID:     "builtin-disk",
			Title:  "Disk Throughput",
			Kind:   "builtin",
			Size:   "normal",
			Series: seriesList,
			Axes: GraphAxes{
				X: Axis{Label: "Time", Mode: "time"},
				Y: Axis{Label: "Throughput", Unit: unit, Symbol: symbol, Mode: "auto", BeginZero: true},
			},
		})
	}
	add("builtin-disk-space", "Disk Space", "Storage", "filesystem_used_bytes", "filesystem_free_bytes")
	add("builtin-processes", "Processes", "Processes", "process_running", "process_sleeping", "process_blocked", "process_threads")
	return graphs
}

func graphSeries(metrics []series.Metric, names ...string) []GraphSeries {
	nameSet := map[string]bool{}
	for _, name := range names {
		nameSet[name] = true
	}
	return graphSeriesByPredicate(metrics, func(metric series.Metric) bool {
		return nameSet[metric.Name]
	})
}

func graphSeriesByPredicate(metrics []series.Metric, include func(series.Metric) bool) []GraphSeries {
	preferredGPUCollectors := preferredGPUCollectors(metrics)
	out := []GraphSeries{}
	for _, metric := range metrics {
		if !include(metric) || skipFallbackGPUMetric(metric, preferredGPUCollectors) {
			continue
		}
		transform := ""
		unit := metric.Unit
		symbol := metric.Symbol
		if strings.HasSuffix(metric.Name, "_total") {
			transform = "rate"
			if unit == "byte" {
				symbol = "B/s"
			} else if symbol != "" {
				symbol += "/s"
			} else {
				symbol = "rate"
			}
		}
		legend := metricLegend(metric, transform)
		out = append(out, GraphSeries{
			ID:        graphID("series", metric),
			Metric:    metric,
			Legend:    legend,
			Color:     graphColor(len(out)),
			Axis:      "y",
			Transform: transform,
			Labels:    metric.Labels,
		})
		out[len(out)-1].Metric.Symbol = symbol
		out[len(out)-1].Metric.Unit = unit
	}
	slices.SortFunc(out, func(a, b GraphSeries) int {
		if a.Metric.Name < b.Metric.Name {
			return -1
		}
		if a.Metric.Name > b.Metric.Name {
			return 1
		}
		if a.Legend < b.Legend {
			return -1
		}
		if a.Legend > b.Legend {
			return 1
		}
		return 0
	})
	for i := range out {
		out[i].Color = graphColor(i)
	}
	return out
}

func isPowerMetric(metric series.Metric) bool {
	if metric.Unit != "watt" && metric.Symbol != "W" {
		return false
	}
	if metric.Kind == "power_limit" {
		return false
	}
	name := strings.ToLower(metric.Name)
	return !strings.Contains(name, "limit") && !strings.Contains(name, "cap")
}

func isDefaultDiskThroughputMetric(metric series.Metric) bool {
	switch metric.Name {
	case "disk_read_bytes_per_second", "disk_written_bytes_per_second":
	default:
		return false
	}
	return !isPartitionDevice(metric.Labels["device"])
}

func isPartitionDevice(device string) bool {
	if device == "" {
		return false
	}
	for i := len(device) - 1; i >= 0; i-- {
		if device[i] < '0' || device[i] > '9' {
			if i == len(device)-1 {
				return false
			}
			if device[i] == 'p' {
				return true
			}
			return strings.HasPrefix(device, "sd") || strings.HasPrefix(device, "vd") || strings.HasPrefix(device, "xvd") || strings.HasPrefix(device, "mmcblk")
		}
	}
	return false
}

func preferredGPUCollectors(metrics []series.Metric) map[string]string {
	preferred := map[string]string{}
	for _, metric := range metrics {
		if metric.Labels["collector"] != "drm" {
			continue
		}
		switch metric.Name {
		case "gpu_power_w", "gpu_util_pct":
			preferred[metric.Name] = "drm"
		}
	}
	return preferred
}

func skipFallbackGPUMetric(metric series.Metric, preferred map[string]string) bool {
	if metric.Labels["collector"] != "rocm" {
		return false
	}
	switch metric.Name {
	case "gpu_power_w", "gpu_util_pct":
		return preferred[metric.Name] == "drm"
	default:
		return false
	}
}

func commonUnit(seriesList []GraphSeries) (string, string) {
	if len(seriesList) == 0 {
		return "", ""
	}
	if allByteRateUnits(seriesList) {
		return "byte/second", "B/s"
	}
	if allMemoryUnits(seriesList) {
		return "byte", "B"
	}
	unit := seriesList[0].Metric.Unit
	symbol := seriesList[0].Metric.Symbol
	for _, item := range seriesList[1:] {
		if item.Metric.Unit != unit {
			return "", ""
		}
		if item.Metric.Symbol != symbol {
			return unit, ""
		}
	}
	return unit, symbol
}

func allMemoryUnits(seriesList []GraphSeries) bool {
	for _, item := range seriesList {
		switch item.Metric.Symbol {
		case "B", "MiB":
			continue
		default:
			return false
		}
	}
	return true
}

func allByteRateUnits(seriesList []GraphSeries) bool {
	for _, item := range seriesList {
		switch item.Metric.Symbol {
		case "B/s", "MiB/s":
			continue
		default:
			return false
		}
	}
	return true
}

func beginZero(kind string) bool {
	switch strings.ToLower(kind) {
	case "battery", "connections", "power", "utilization", "memory", "storage", "throughput", "processes", "activity":
		return true
	default:
		return false
	}
}
