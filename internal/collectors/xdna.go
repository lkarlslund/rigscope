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
		Name: "xdna",
		Detect: func() (Collector, bool, error) {
			collector := XDNA{Root: "/sys/class/accel"}
			ok, err := collector.detect()
			return collector, ok, err
		},
	})
}

type XDNA struct {
	Root string
}

func (XDNA) Name() string { return "xdna" }

func (c XDNA) detect() (bool, error) {
	entries, err := os.ReadDir(c.Root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "accel") {
			continue
		}
		device := filepath.Join(c.Root, entry.Name(), "device")
		if readDriverName(device) == "amdxdna" {
			return true, nil
		}
	}
	return false, nil
}

func (c XDNA) Sample(context.Context) (map[string]any, error) {
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "accel") {
			continue
		}
		accelPath := filepath.Join(c.Root, entry.Name())
		devicePath := filepath.Join(accelPath, "device")
		if readDriverName(devicePath) != "amdxdna" {
			continue
		}
		labels := xdnaLabels(entry.Name(), accelPath, devicePath)
		m = append(m, metric("npu_present", 1, "count", "count", "presence", labels))
		if value, ok := readMillisAsSeconds(filepath.Join(devicePath, "power", "runtime_active_time")); ok {
			m = append(m, metric("npu_runtime_active_seconds_total", value, "second", "s", "counter", labels))
		}
		if value, ok := readMillisAsSeconds(filepath.Join(devicePath, "power", "runtime_suspended_time")); ok {
			m = append(m, metric("npu_runtime_suspended_seconds_total", value, "second", "s", "counter", labels))
		}
		if value, ok := runtimeStatusValue(readTrim(filepath.Join(devicePath, "power", "runtime_status"))); ok {
			m = append(m, metric("npu_runtime_active", value, "count", "count", "state", labels))
		}
		if value, ok := readFloatFile(filepath.Join(devicePath, "driver", "module", "parameters", "aie2_max_col")); ok {
			m = append(m, metric("npu_aie2_max_col", value, "count", "count", "resource_limit", labels))
		}
	}
	return metricRecord(m), nil
}

func xdnaLabels(accelName string, accelPath string, devicePath string) map[string]string {
	labels := map[string]string{
		"accel":  accelName,
		"driver": readDriverName(devicePath),
		"bdf":    pciBDF(devicePath),
		"vendor": readTrim(filepath.Join(devicePath, "vendor")),
		"device": readTrim(filepath.Join(devicePath, "device")),
		"vbnv":   readTrim(filepath.Join(devicePath, "vbnv")),
	}
	if dev := readTrim(filepath.Join(accelPath, "dev")); dev != "" {
		labels["dev"] = dev
	}
	if class := readTrim(filepath.Join(devicePath, "class")); class != "" {
		labels["class"] = class
	}
	if revision := readTrim(filepath.Join(devicePath, "revision")); revision != "" {
		labels["revision"] = revision
	}
	return labels
}

func pciBDF(devicePath string) string {
	resolved, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return filepath.Base(devicePath)
	}
	return filepath.Base(resolved)
}

func readMillisAsSeconds(path string) (float64, bool) {
	raw := readTrim(path)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return value / 1000, true
}

func runtimeStatusValue(status string) (float64, bool) {
	switch strings.TrimSpace(status) {
	case "active":
		return 1, true
	case "suspended", "suspending":
		return 0, true
	default:
		return 0, false
	}
}
