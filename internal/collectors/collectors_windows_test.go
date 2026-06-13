//go:build windows

package collectors

import (
	"context"
	"testing"
)

func TestWindowsCollectorsSampleMetricRecords(t *testing.T) {
	collectors := []Collector{
		&WindowsCPU{},
		WindowsMemory{},
		WindowsNetwork{},
		WindowsDisk{},
		WindowsFilesystem{},
		WindowsProcess{},
		WindowsSelf{},
	}
	for _, collector := range collectors {
		t.Run(collector.Name(), func(t *testing.T) {
			sample, err := collector.Sample(context.Background())
			if err != nil {
				t.Fatalf("Sample() error = %v", err)
			}
			if _, ok := sample["metrics"].([]map[string]any); !ok {
				t.Fatalf("Sample() = %#v, want metrics record", sample)
			}
		})
	}
}

func TestWindowsNVIDIASamplesNVMLWhenAvailable(t *testing.T) {
	if !nvidiaAvailable() {
		t.Skip("NVML is not available")
	}
	sample, err := (NVIDIA{}).Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	devices, ok := sample["devices"].([]map[string]any)
	if !ok || len(devices) == 0 {
		t.Fatalf("Sample() = %#v, want at least one NVML device", sample)
	}
	device := devices[0]
	for _, key := range []string{"name", "power_w", "temp_c", "util_pct", "mem_used_mib"} {
		if _, ok := device[key]; !ok {
			t.Fatalf("device = %#v, missing %q", device, key)
		}
	}
}
