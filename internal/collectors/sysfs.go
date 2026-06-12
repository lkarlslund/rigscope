package collectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	Register(Registration{
		Name: "thermal",
		Detect: func() (Collector, bool, error) {
			collector := Thermal{
				HwmonRoot:   "/sys/class/hwmon",
				ThermalRoot: "/sys/class/thermal",
			}
			ok, err := collector.detect()
			return collector, ok, err
		},
	})
	Register(Registration{
		Name: "power_supply",
		Detect: func() (Collector, bool, error) {
			collector := PowerSupply{Root: "/sys/class/power_supply"}
			ok, err := collector.detect()
			return collector, ok, err
		},
	})
	Register(Registration{
		Name: "drm",
		Detect: func() (Collector, bool, error) {
			collector := DRM{Root: "/sys/class/drm"}
			ok, err := collector.detect()
			return collector, ok, err
		},
	})
}

type Thermal struct {
	HwmonRoot   string
	ThermalRoot string
}

func (Thermal) Name() string { return "thermal" }

func (c Thermal) detect() (bool, error) {
	matches, err := filepath.Glob(filepath.Join(c.HwmonRoot, "hwmon*", "temp*_input"))
	if err != nil {
		return false, err
	}
	if len(matches) > 0 {
		return true, nil
	}
	matches, err = filepath.Glob(filepath.Join(c.ThermalRoot, "thermal_zone*", "temp"))
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

func (c Thermal) Sample(context.Context) (map[string]any, error) {
	m := []map[string]any{}
	hwmon, err := c.hwmonMetrics()
	if err != nil {
		return nil, err
	}
	m = append(m, hwmon...)
	zones, err := c.thermalZoneMetrics()
	if err != nil {
		return nil, err
	}
	m = append(m, zones...)
	return metricRecord(m), nil
}

func (c Thermal) hwmonMetrics() ([]map[string]any, error) {
	dirs, err := filepath.Glob(filepath.Join(c.HwmonRoot, "hwmon*"))
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, dir := range dirs {
		chip := readTrim(filepath.Join(dir, "name"))
		tempInputs, err := filepath.Glob(filepath.Join(dir, "temp*_input"))
		if err != nil {
			return nil, err
		}
		for _, input := range tempInputs {
			prefix := strings.TrimSuffix(input, "_input")
			temp, ok := readMilli(input)
			if !ok {
				continue
			}
			label := readTrim(prefix + "_label")
			if label == "" {
				label = filepath.Base(prefix)
			}
			m = append(m, metric("temperature_celsius", temp, "celsius", "°C", "temperature", map[string]string{
				"source": "hwmon",
				"chip":   chip,
				"sensor": label,
			}))
		}
		if chip == "amdgpu" || chip == "zenpower" {
			continue
		}
		powerInputs, err := filepath.Glob(filepath.Join(dir, "power*_input"))
		if err != nil {
			return nil, err
		}
		powerAverages, err := filepath.Glob(filepath.Join(dir, "power*_average"))
		if err != nil {
			return nil, err
		}
		seenPower := map[string]bool{}
		for _, input := range append(powerInputs, powerAverages...) {
			prefix := strings.TrimSuffix(strings.TrimSuffix(input, "_input"), "_average")
			if seenPower[prefix] {
				continue
			}
			seenPower[prefix] = true
			value, ok := readMicro(input)
			if !ok {
				continue
			}
			label := readTrim(prefix + "_label")
			if label == "" {
				label = filepath.Base(prefix)
			}
			m = append(m, metric("hwmon_power_w", value, "watt", "W", "power", map[string]string{
				"source": "hwmon",
				"chip":   chip,
				"sensor": label,
			}))
		}
	}
	return m, nil
}

func (c Thermal) thermalZoneMetrics() ([]map[string]any, error) {
	dirs, err := filepath.Glob(filepath.Join(c.ThermalRoot, "thermal_zone*"))
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, dir := range dirs {
		temp, ok := readMilli(filepath.Join(dir, "temp"))
		if !ok {
			continue
		}
		zoneType := readTrim(filepath.Join(dir, "type"))
		m = append(m, metric("temperature_celsius", temp, "celsius", "°C", "temperature", map[string]string{
			"source": "thermal_zone",
			"zone":   filepath.Base(dir),
			"type":   zoneType,
		}))
	}
	return m, nil
}

type PowerSupply struct {
	Root string
}

func (PowerSupply) Name() string { return "power_supply" }

func (c PowerSupply) detect() (bool, error) {
	entries, err := os.ReadDir(c.Root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func (c PowerSupply) Sample(context.Context) (map[string]any, error) {
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		dir := filepath.Join(c.Root, entry.Name())
		labels := map[string]string{
			"supply": entry.Name(),
			"type":   readTrim(filepath.Join(dir, "type")),
			"status": readTrim(filepath.Join(dir, "status")),
		}
		if value, ok := readFloatFile(filepath.Join(dir, "capacity")); ok {
			m = append(m, metric("power_supply_capacity_pct", value, "percent", "%", "battery", labels))
		}
		if value, ok := readMicro(filepath.Join(dir, "power_now")); ok {
			m = append(m, metric("power_supply_power_w", value, "watt", "W", "power", labels))
		}
		if value, ok := readMicro(filepath.Join(dir, "energy_now")); ok {
			m = append(m, metric("power_supply_energy_wh", value, "watt-hour", "Wh", "energy", labels))
		}
		if value, ok := readMicro(filepath.Join(dir, "energy_full")); ok {
			m = append(m, metric("power_supply_energy_full_wh", value, "watt-hour", "Wh", "energy", labels))
		}
		if value, ok := readMilli(filepath.Join(dir, "temp")); ok {
			m = append(m, metric("power_supply_temp_celsius", value, "celsius", "°C", "temperature", labels))
		}
	}
	return metricRecord(m), nil
}

type DRM struct {
	Root string
}

func (DRM) Name() string { return "drm" }

func (c DRM) detect() (bool, error) {
	matches, err := filepath.Glob(filepath.Join(c.Root, "card*"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, path := range matches {
		if _, err := os.Stat(filepath.Join(path, "device")); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func (c DRM) hasDriver(driver string) bool {
	cards, err := filepath.Glob(filepath.Join(c.Root, "card*"))
	if err != nil {
		return false
	}
	for _, card := range cards {
		if readDriverName(filepath.Join(card, "device")) == driver {
			return true
		}
	}
	return false
}

func (c DRM) Sample(context.Context) (map[string]any, error) {
	cards, err := filepath.Glob(filepath.Join(c.Root, "card*"))
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, card := range cards {
		device := filepath.Join(card, "device")
		if _, err := os.Stat(device); err != nil {
			continue
		}
		labels := map[string]string{
			"card":   filepath.Base(card),
			"driver": readDriverName(device),
			"name":   readTrim(filepath.Join(device, "product_name")),
		}
		if value, ok := readFloatFile(filepath.Join(device, "gpu_busy_percent")); ok {
			m = append(m, metric("gpu_util_pct", value, "percent", "%", "utilization", labels))
		}
		if value, ok := readBytesFile(filepath.Join(device, "mem_info_vram_total")); ok {
			m = append(m, metric("gpu_vram_total_bytes", value, "byte", "B", "memory", labels))
		}
		if value, ok := readBytesFile(filepath.Join(device, "mem_info_vram_used")); ok {
			m = append(m, metric("gpu_vram_used_bytes", value, "byte", "B", "memory", labels))
		}
		if value, ok := readBytesFile(filepath.Join(device, "mem_info_gtt_total")); ok {
			m = append(m, metric("gpu_gtt_total_bytes", value, "byte", "B", "memory", labels))
		}
		if value, ok := readBytesFile(filepath.Join(device, "mem_info_gtt_used")); ok {
			m = append(m, metric("gpu_gtt_used_bytes", value, "byte", "B", "memory", labels))
		}
		m = append(m, drmHwmonMetrics(device, labels)...)
	}
	return metricRecord(m), nil
}

func drmHwmonMetrics(device string, labels map[string]string) []map[string]any {
	hwmons, _ := filepath.Glob(filepath.Join(device, "hwmon", "hwmon*"))
	m := []map[string]any{}
	for _, hwmon := range hwmons {
		cardLabels := cloneLabels(labels)
		cardLabels["chip"] = readTrim(filepath.Join(hwmon, "name"))
		if value, ok := readMicro(filepath.Join(hwmon, "power1_average")); ok {
			m = append(m, metric("gpu_power_w", value, "watt", "W", "power", cardLabels))
		}
		if value, ok := readMicro(filepath.Join(hwmon, "power1_cap")); ok {
			m = append(m, metric("gpu_power_limit_w", value, "watt", "W", "power_limit", cardLabels))
		}
		inputs, _ := filepath.Glob(filepath.Join(hwmon, "temp*_input"))
		for _, input := range inputs {
			temp, ok := readMilli(input)
			if !ok {
				continue
			}
			tempLabels := cloneLabels(cardLabels)
			tempLabels["sensor"] = readTrim(strings.TrimSuffix(input, "_input") + "_label")
			if tempLabels["sensor"] == "" {
				tempLabels["sensor"] = filepath.Base(strings.TrimSuffix(input, "_input"))
			}
			m = append(m, metric("gpu_temp_celsius", temp, "celsius", "°C", "temperature", tempLabels))
		}
	}
	return m
}

func readDriverName(device string) string {
	target, err := os.Readlink(filepath.Join(device, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readFloatFile(path string) (float64, bool) {
	raw := readTrim(path)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	return value, err == nil
}

func readMilli(path string) (float64, bool) {
	value, ok := readFloatFile(path)
	if !ok {
		return 0, false
	}
	return value / 1000, true
}

func readMicro(path string) (float64, bool) {
	value, ok := readFloatFile(path)
	if !ok {
		return 0, false
	}
	return value / 1_000_000, true
}

func readBytesFile(path string) (float64, bool) {
	return readFloatFile(path)
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
