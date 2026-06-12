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
