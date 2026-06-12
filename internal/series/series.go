package series

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nakabonne/tstorage"
)

type Metric struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Unit   string            `json:"unit,omitempty"`
	Symbol string            `json:"symbol,omitempty"`
	Kind   string            `json:"kind,omitempty"`
}

type Point struct {
	Metric
	Value float64 `json:"value"`
}

func (m Metric) Key() string {
	if len(m.Labels) == 0 {
		return m.Name
	}
	keys := make([]string, 0, len(m.Labels))
	for key := range m.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(m.Name)
	for _, key := range keys {
		b.WriteByte('{')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(m.Labels[key])
		b.WriteByte('}')
	}
	return b.String()
}

func (m Metric) TSLabels() []tstorage.Label {
	if len(m.Labels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.Labels))
	for key := range m.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	labels := make([]tstorage.Label, 0, len(keys))
	for _, key := range keys {
		if key == "" || m.Labels[key] == "" {
			continue
		}
		labels = append(labels, tstorage.Label{Name: key, Value: m.Labels[key]})
	}
	return labels
}

func FlattenSample(sample map[string]any) []Point {
	var points []Point
	for _, collector := range sampleCollectors(sample["collectors"]) {
		name, _ := collector["collector"].(string)
		points = append(points, flattenGenericMetrics(name, collector)...)
		switch name {
		case "nvidia", "rocm":
			points = append(points, flattenGPUCollector(name, collector)...)
		case "zenpower":
			if value, ok := number(collector["cpu_package_power_w"]); ok {
				points = append(points, Point{
					Metric: Metric{
						Name: "cpu_package_power_w",
						Labels: map[string]string{
							"collector": name,
						},
						Unit:   "watt",
						Symbol: "W",
						Kind:   "power",
					},
					Value: value,
				})
			}
		}
	}
	return points
}

func flattenGPUCollector(collectorName string, collector map[string]any) []Point {
	devices := sampleDevices(collector["devices"])
	points := make([]Point, 0, len(devices)*12)
	for _, dev := range devices {
		labels := map[string]string{
			"collector": collectorName,
			"index":     fmt.Sprint(dev["index"]),
		}
		if deviceName, ok := dev["name"].(string); ok && deviceName != "" {
			labels["device"] = deviceName
		}
		appendMetric := func(name, unit, symbol, kind string, raw any) {
			value, ok := number(raw)
			if !ok {
				return
			}
			metricLabels := make(map[string]string, len(labels))
			for key, val := range labels {
				metricLabels[key] = val
			}
			points = append(points, Point{
				Metric: Metric{Name: name, Labels: metricLabels, Unit: unit, Symbol: symbol, Kind: kind},
				Value:  value,
			})
		}
		appendMetric("gpu_power_w", "watt", "W", "power", dev["power_w"])
		appendMetric("gpu_power_limit_w", "watt", "W", "power_limit", dev["power_limit_w"])
		appendMetric("gpu_sm_clock_mhz", "megahertz", "MHz", "clock", dev["sm_clock_mhz"])
		appendMetric("gpu_mem_clock_mhz", "megahertz", "MHz", "clock", dev["mem_clock_mhz"])
		appendMetric("gpu_temp_c", "celsius", "°C", "temperature", dev["temp_c"])
		appendMetric("gpu_util_pct", "percent", "%", "utilization", dev["util_pct"])
		appendMetric("gpu_mem_util_pct", "percent", "%", "utilization", dev["mem_util_pct"])
		appendMetric("gpu_mem_used_mib", "mebibyte", "MiB", "memory", dev["mem_used_mib"])
		appendMetric("gpu_vram_total_bytes", "byte", "B", "memory", dev["vram_total_bytes"])
		appendMetric("gpu_vram_used_bytes", "byte", "B", "memory", dev["vram_used_bytes"])
		appendMetric("gpu_gtt_total_bytes", "byte", "B", "memory", dev["gtt_total_bytes"])
		appendMetric("gpu_gtt_used_bytes", "byte", "B", "memory", dev["gtt_used_bytes"])
	}
	return points
}

func flattenGenericMetrics(collectorName string, collector map[string]any) []Point {
	metrics := sampleMetrics(collector["metrics"])
	points := make([]Point, 0, len(metrics))
	for _, rawMetric := range metrics {
		value, ok := number(rawMetric["value"])
		if !ok {
			continue
		}
		name, _ := rawMetric["name"].(string)
		if name == "" {
			continue
		}
		labels := map[string]string{"collector": collectorName}
		for key, value := range stringMap(rawMetric["labels"]) {
			if key != "" && value != "" {
				labels[key] = value
			}
		}
		unit, _ := rawMetric["unit"].(string)
		symbol, _ := rawMetric["symbol"].(string)
		kind, _ := rawMetric["kind"].(string)
		points = append(points, Point{
			Metric: Metric{Name: name, Labels: labels, Unit: unit, Symbol: symbol, Kind: kind},
			Value:  value,
		})
	}
	return points
}

func sampleCollectors(raw any) []map[string]any {
	switch values := raw.(type) {
	case []map[string]any:
		return values
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if mapped, ok := value.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func sampleDevices(raw any) []map[string]any {
	switch values := raw.(type) {
	case []map[string]any:
		return values
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if mapped, ok := value.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func sampleMetrics(raw any) []map[string]any {
	switch values := raw.(type) {
	case []map[string]any:
		return values
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if mapped, ok := value.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func stringMap(raw any) map[string]string {
	switch values := raw.(type) {
	case map[string]string:
		return values
	case map[string]any:
		out := make(map[string]string, len(values))
		for key, value := range values {
			out[key] = fmt.Sprint(value)
		}
		return out
	default:
		return nil
	}
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	default:
		return 0, false
	}
}
