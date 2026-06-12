//go:build !linux || !cgo

package collectors

import (
	"context"
	"fmt"
)

func rocmAvailable() bool {
	return false
}

func sampleROCM(context.Context) (map[string]any, error) {
	return nil, fmt.Errorf("ROCm SMI collector requires linux with cgo")
}
