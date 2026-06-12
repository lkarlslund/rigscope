//go:build (!linux || !cgo) && !windows

package collectors

import (
	"context"
	"fmt"
)

func nvidiaAvailable() bool {
	return false
}

func sampleNVIDIA(context.Context) (map[string]any, error) {
	return nil, fmt.Errorf("NVML collector requires linux with cgo")
}
