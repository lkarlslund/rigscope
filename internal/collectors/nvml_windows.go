//go:build windows

package collectors

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

type nvmlUtilization struct {
	GPU    uint32
	Memory uint32
}

type nvmlMemory struct {
	Total uint64
	Free  uint64
	Used  uint64
}

var (
	nvmlMu sync.Mutex

	nvmlDLL                           = syscall.NewLazyDLL("nvml.dll")
	nvmlInitV2                        = nvmlDLL.NewProc("nvmlInit_v2")
	nvmlShutdown                      = nvmlDLL.NewProc("nvmlShutdown")
	nvmlDeviceGetCountV2              = nvmlDLL.NewProc("nvmlDeviceGetCount_v2")
	nvmlDeviceGetHandleByIndexV2      = nvmlDLL.NewProc("nvmlDeviceGetHandleByIndex_v2")
	nvmlDeviceGetName                 = nvmlDLL.NewProc("nvmlDeviceGetName")
	nvmlDeviceGetPowerUsage           = nvmlDLL.NewProc("nvmlDeviceGetPowerUsage")
	nvmlDeviceGetPowerManagementLimit = nvmlDLL.NewProc("nvmlDeviceGetPowerManagementLimit")
	nvmlDeviceGetClockInfo            = nvmlDLL.NewProc("nvmlDeviceGetClockInfo")
	nvmlDeviceGetTemperature          = nvmlDLL.NewProc("nvmlDeviceGetTemperature")
	nvmlDeviceGetUtilizationRates     = nvmlDLL.NewProc("nvmlDeviceGetUtilizationRates")
	nvmlDeviceGetMemoryInfo           = nvmlDLL.NewProc("nvmlDeviceGetMemoryInfo")
)

func nvidiaAvailable() bool {
	nvmlMu.Lock()
	defer nvmlMu.Unlock()

	if !nvmlInit() {
		return false
	}
	var count uint32
	return nvmlCall(nvmlDeviceGetCountV2, uintptr(unsafe.Pointer(&count))) && count > 0
}

func sampleNVIDIA(context.Context) (map[string]any, error) {
	nvmlMu.Lock()
	defer nvmlMu.Unlock()

	if !nvmlInit() {
		return nil, fmt.Errorf("NVML unavailable")
	}

	var count uint32
	if !nvmlCall(nvmlDeviceGetCountV2, uintptr(unsafe.Pointer(&count))) {
		return nil, fmt.Errorf("NVML device count unavailable")
	}

	devices := make([]map[string]any, 0, int(count))
	for i := uint32(0); i < count; i++ {
		var device uintptr
		if !nvmlCall(nvmlDeviceGetHandleByIndexV2, uintptr(i), uintptr(unsafe.Pointer(&device))) {
			continue
		}

		record := map[string]any{"index": int(i)}

		name := make([]byte, 96)
		if nvmlCall(nvmlDeviceGetName, device, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name))) {
			record["name"] = strings.TrimRight(string(name), "\x00")
		}

		var value uint32
		if nvmlCall(nvmlDeviceGetPowerUsage, device, uintptr(unsafe.Pointer(&value))) {
			record["power_w"] = float64(value) / 1000
		}
		if nvmlCall(nvmlDeviceGetPowerManagementLimit, device, uintptr(unsafe.Pointer(&value))) {
			record["power_limit_w"] = float64(value) / 1000
		}
		if nvmlCall(nvmlDeviceGetClockInfo, device, uintptr(1), uintptr(unsafe.Pointer(&value))) {
			record["sm_clock_mhz"] = float64(value)
		}
		if nvmlCall(nvmlDeviceGetClockInfo, device, uintptr(2), uintptr(unsafe.Pointer(&value))) {
			record["mem_clock_mhz"] = float64(value)
		}
		if nvmlCall(nvmlDeviceGetTemperature, device, uintptr(0), uintptr(unsafe.Pointer(&value))) {
			record["temp_c"] = float64(value)
		}

		var utilization nvmlUtilization
		if nvmlCall(nvmlDeviceGetUtilizationRates, device, uintptr(unsafe.Pointer(&utilization))) {
			record["util_pct"] = float64(utilization.GPU)
			record["mem_util_pct"] = float64(utilization.Memory)
		}

		var memory nvmlMemory
		if nvmlCall(nvmlDeviceGetMemoryInfo, device, uintptr(unsafe.Pointer(&memory))) {
			record["mem_used_mib"] = float64(memory.Used) / 1024 / 1024
			record["vram_total_bytes"] = float64(memory.Total)
			record["vram_used_bytes"] = float64(memory.Used)
		}

		devices = append(devices, record)
	}

	return map[string]any{"devices": devices}, nil
}

func nvmlInit() bool {
	if err := nvmlInitV2.Find(); err != nil {
		return false
	}
	return nvmlCall(nvmlInitV2)
}

func nvmlCall(proc *syscall.LazyProc, args ...uintptr) bool {
	if err := proc.Find(); err != nil {
		return false
	}
	result, _, _ := proc.Call(args...)
	return result == 0
}
