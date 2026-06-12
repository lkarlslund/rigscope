//go:build linux && cgo

package collectors

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

typedef void* nvmlDevice_t;
typedef int nvmlReturn_t;

typedef struct {
	unsigned int gpu;
	unsigned int memory;
} nvmlUtilization_t;

typedef struct {
	unsigned long long total;
	unsigned long long free;
	unsigned long long used;
} nvmlMemory_t;

typedef nvmlReturn_t (*nvmlInit_v2_fn)(void);
typedef nvmlReturn_t (*nvmlShutdown_fn)(void);
typedef nvmlReturn_t (*nvmlDeviceGetCount_v2_fn)(unsigned int *);
typedef nvmlReturn_t (*nvmlDeviceGetHandleByIndex_v2_fn)(unsigned int, nvmlDevice_t *);
typedef nvmlReturn_t (*nvmlDeviceGetName_fn)(nvmlDevice_t, char *, unsigned int);
typedef nvmlReturn_t (*nvmlDeviceGetPowerUsage_fn)(nvmlDevice_t, unsigned int *);
typedef nvmlReturn_t (*nvmlDeviceGetPowerManagementLimit_fn)(nvmlDevice_t, unsigned int *);
typedef nvmlReturn_t (*nvmlDeviceGetClockInfo_fn)(nvmlDevice_t, int, unsigned int *);
typedef nvmlReturn_t (*nvmlDeviceGetTemperature_fn)(nvmlDevice_t, int, unsigned int *);
typedef nvmlReturn_t (*nvmlDeviceGetUtilizationRates_fn)(nvmlDevice_t, nvmlUtilization_t *);
typedef nvmlReturn_t (*nvmlDeviceGetMemoryInfo_fn)(nvmlDevice_t, nvmlMemory_t *);

static void *rigscope_nvml_handle;
static nvmlInit_v2_fn rigscope_nvmlInit_v2;
static nvmlShutdown_fn rigscope_nvmlShutdown;
static nvmlDeviceGetCount_v2_fn rigscope_nvmlDeviceGetCount_v2;
static nvmlDeviceGetHandleByIndex_v2_fn rigscope_nvmlDeviceGetHandleByIndex_v2;
static nvmlDeviceGetName_fn rigscope_nvmlDeviceGetName;
static nvmlDeviceGetPowerUsage_fn rigscope_nvmlDeviceGetPowerUsage;
static nvmlDeviceGetPowerManagementLimit_fn rigscope_nvmlDeviceGetPowerManagementLimit;
static nvmlDeviceGetClockInfo_fn rigscope_nvmlDeviceGetClockInfo;
static nvmlDeviceGetTemperature_fn rigscope_nvmlDeviceGetTemperature;
static nvmlDeviceGetUtilizationRates_fn rigscope_nvmlDeviceGetUtilizationRates;
static nvmlDeviceGetMemoryInfo_fn rigscope_nvmlDeviceGetMemoryInfo;

static void *rigscope_symbol(const char *name) {
	return dlsym(rigscope_nvml_handle, name);
}

static int rigscope_nvml_open(void) {
	if (rigscope_nvml_handle != NULL) {
		return 1;
	}
	rigscope_nvml_handle = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_LOCAL);
	if (rigscope_nvml_handle == NULL) {
		rigscope_nvml_handle = dlopen("libnvidia-ml.so", RTLD_LAZY | RTLD_LOCAL);
	}
	if (rigscope_nvml_handle == NULL) {
		return 0;
	}

	rigscope_nvmlInit_v2 = (nvmlInit_v2_fn)rigscope_symbol("nvmlInit_v2");
	rigscope_nvmlShutdown = (nvmlShutdown_fn)rigscope_symbol("nvmlShutdown");
	rigscope_nvmlDeviceGetCount_v2 = (nvmlDeviceGetCount_v2_fn)rigscope_symbol("nvmlDeviceGetCount_v2");
	rigscope_nvmlDeviceGetHandleByIndex_v2 = (nvmlDeviceGetHandleByIndex_v2_fn)rigscope_symbol("nvmlDeviceGetHandleByIndex_v2");
	rigscope_nvmlDeviceGetName = (nvmlDeviceGetName_fn)rigscope_symbol("nvmlDeviceGetName");
	rigscope_nvmlDeviceGetPowerUsage = (nvmlDeviceGetPowerUsage_fn)rigscope_symbol("nvmlDeviceGetPowerUsage");
	rigscope_nvmlDeviceGetPowerManagementLimit = (nvmlDeviceGetPowerManagementLimit_fn)rigscope_symbol("nvmlDeviceGetPowerManagementLimit");
	rigscope_nvmlDeviceGetClockInfo = (nvmlDeviceGetClockInfo_fn)rigscope_symbol("nvmlDeviceGetClockInfo");
	rigscope_nvmlDeviceGetTemperature = (nvmlDeviceGetTemperature_fn)rigscope_symbol("nvmlDeviceGetTemperature");
	rigscope_nvmlDeviceGetUtilizationRates = (nvmlDeviceGetUtilizationRates_fn)rigscope_symbol("nvmlDeviceGetUtilizationRates");
	rigscope_nvmlDeviceGetMemoryInfo = (nvmlDeviceGetMemoryInfo_fn)rigscope_symbol("nvmlDeviceGetMemoryInfo");

	if (rigscope_nvmlInit_v2 == NULL ||
		rigscope_nvmlDeviceGetCount_v2 == NULL ||
		rigscope_nvmlDeviceGetHandleByIndex_v2 == NULL ||
		rigscope_nvmlDeviceGetName == NULL) {
		return 0;
	}
	return 1;
}

static int rigscope_nvml_init(void) {
	if (!rigscope_nvml_open()) {
		return 0;
	}
	return rigscope_nvmlInit_v2() == 0;
}

static int rigscope_nvml_count(unsigned int *count) {
	return rigscope_nvmlDeviceGetCount_v2(count) == 0;
}

static int rigscope_nvml_handle_by_index(unsigned int index, nvmlDevice_t *device) {
	return rigscope_nvmlDeviceGetHandleByIndex_v2(index, device) == 0;
}

static int rigscope_nvml_name(nvmlDevice_t device, char *name, unsigned int len) {
	return rigscope_nvmlDeviceGetName(device, name, len) == 0;
}

static int rigscope_nvml_power_usage(nvmlDevice_t device, unsigned int *value) {
	return rigscope_nvmlDeviceGetPowerUsage != NULL && rigscope_nvmlDeviceGetPowerUsage(device, value) == 0;
}

static int rigscope_nvml_power_limit(nvmlDevice_t device, unsigned int *value) {
	return rigscope_nvmlDeviceGetPowerManagementLimit != NULL && rigscope_nvmlDeviceGetPowerManagementLimit(device, value) == 0;
}

static int rigscope_nvml_clock(nvmlDevice_t device, int clock_type, unsigned int *value) {
	return rigscope_nvmlDeviceGetClockInfo != NULL && rigscope_nvmlDeviceGetClockInfo(device, clock_type, value) == 0;
}

static int rigscope_nvml_temperature(nvmlDevice_t device, unsigned int *value) {
	return rigscope_nvmlDeviceGetTemperature != NULL && rigscope_nvmlDeviceGetTemperature(device, 0, value) == 0;
}

static int rigscope_nvml_utilization(nvmlDevice_t device, nvmlUtilization_t *value) {
	return rigscope_nvmlDeviceGetUtilizationRates != NULL && rigscope_nvmlDeviceGetUtilizationRates(device, value) == 0;
}

static int rigscope_nvml_memory(nvmlDevice_t device, nvmlMemory_t *value) {
	return rigscope_nvmlDeviceGetMemoryInfo != NULL && rigscope_nvmlDeviceGetMemoryInfo(device, value) == 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
)

var nvmlMu sync.Mutex

func nvidiaAvailable() bool {
	nvmlMu.Lock()
	defer nvmlMu.Unlock()

	if C.rigscope_nvml_init() == 0 {
		return false
	}
	var count C.uint
	return C.rigscope_nvml_count(&count) != 0 && count > 0
}

func sampleNVIDIA(context.Context) (map[string]any, error) {
	nvmlMu.Lock()
	defer nvmlMu.Unlock()

	if C.rigscope_nvml_init() == 0 {
		return nil, fmt.Errorf("NVML unavailable")
	}

	var count C.uint
	if C.rigscope_nvml_count(&count) == 0 {
		return nil, fmt.Errorf("NVML device count unavailable")
	}

	devices := make([]map[string]any, 0, int(count))
	for i := C.uint(0); i < count; i++ {
		var device C.nvmlDevice_t
		if C.rigscope_nvml_handle_by_index(i, &device) == 0 {
			continue
		}

		record := map[string]any{"index": int(i)}

		name := make([]C.char, 96)
		if C.rigscope_nvml_name(device, &name[0], C.uint(len(name))) != 0 {
			record["name"] = C.GoString(&name[0])
		}

		var value C.uint
		if C.rigscope_nvml_power_usage(device, &value) != 0 {
			record["power_w"] = float64(value) / 1000
		}
		if C.rigscope_nvml_power_limit(device, &value) != 0 {
			record["power_limit_w"] = float64(value) / 1000
		}
		if C.rigscope_nvml_clock(device, 1, &value) != 0 {
			record["sm_clock_mhz"] = float64(value)
		}
		if C.rigscope_nvml_clock(device, 2, &value) != 0 {
			record["mem_clock_mhz"] = float64(value)
		}
		if C.rigscope_nvml_temperature(device, &value) != 0 {
			record["temp_c"] = float64(value)
		}

		var utilization C.nvmlUtilization_t
		if C.rigscope_nvml_utilization(device, &utilization) != 0 {
			record["util_pct"] = float64(utilization.gpu)
		}

		var memory C.nvmlMemory_t
		if C.rigscope_nvml_memory(device, &memory) != 0 {
			record["mem_used_mib"] = float64(uint64(memory.used)) / 1024 / 1024
		}

		devices = append(devices, record)
	}

	return map[string]any{"devices": devices}, nil
}
