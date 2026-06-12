//go:build linux && cgo

package collectors

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

typedef int rsmi_status_t;
typedef int RSMI_POWER_TYPE;

typedef rsmi_status_t (*rsmi_init_fn)(uint64_t);
typedef rsmi_status_t (*rsmi_shut_down_fn)(void);
typedef rsmi_status_t (*rsmi_num_monitor_devices_fn)(uint32_t *);
typedef rsmi_status_t (*rsmi_dev_name_get_fn)(uint32_t, char *, size_t);
typedef rsmi_status_t (*rsmi_dev_power_get_fn)(uint32_t, uint64_t *, RSMI_POWER_TYPE *);
typedef rsmi_status_t (*rsmi_dev_power_cap_get_fn)(uint32_t, uint32_t, uint64_t *);
typedef rsmi_status_t (*rsmi_dev_busy_percent_get_fn)(uint32_t, uint32_t *);
typedef rsmi_status_t (*rsmi_dev_temp_metric_get_fn)(uint32_t, uint32_t, int, int64_t *);
typedef rsmi_status_t (*rsmi_dev_memory_usage_get_fn)(uint32_t, int, uint64_t *);
typedef rsmi_status_t (*rsmi_dev_memory_total_get_fn)(uint32_t, int, uint64_t *);
typedef rsmi_status_t (*rsmi_dev_memory_busy_percent_get_fn)(uint32_t, uint32_t *);

static void *rigscope_rsmi_handle;
static rsmi_init_fn rigscope_rsmi_init;
static rsmi_shut_down_fn rigscope_rsmi_shut_down;
static rsmi_num_monitor_devices_fn rigscope_rsmi_num_monitor_devices;
static rsmi_dev_name_get_fn rigscope_rsmi_dev_name_get;
static rsmi_dev_power_get_fn rigscope_rsmi_dev_power_get;
static rsmi_dev_power_cap_get_fn rigscope_rsmi_dev_power_cap_get;
static rsmi_dev_busy_percent_get_fn rigscope_rsmi_dev_busy_percent_get;
static rsmi_dev_temp_metric_get_fn rigscope_rsmi_dev_temp_metric_get;
static rsmi_dev_memory_usage_get_fn rigscope_rsmi_dev_memory_usage_get;
static rsmi_dev_memory_total_get_fn rigscope_rsmi_dev_memory_total_get;
static rsmi_dev_memory_busy_percent_get_fn rigscope_rsmi_dev_memory_busy_percent_get;

static void *rigscope_rsmi_symbol(const char *name) {
	return dlsym(rigscope_rsmi_handle, name);
}

static int rigscope_rsmi_open(void) {
	if (rigscope_rsmi_handle != NULL) {
		return 1;
	}
	rigscope_rsmi_handle = dlopen("librocm_smi64.so.1", RTLD_LAZY | RTLD_LOCAL);
	if (rigscope_rsmi_handle == NULL) {
		rigscope_rsmi_handle = dlopen("librocm_smi64.so", RTLD_LAZY | RTLD_LOCAL);
	}
	if (rigscope_rsmi_handle == NULL) {
		return 0;
	}

	rigscope_rsmi_init = (rsmi_init_fn)rigscope_rsmi_symbol("rsmi_init");
	rigscope_rsmi_shut_down = (rsmi_shut_down_fn)rigscope_rsmi_symbol("rsmi_shut_down");
	rigscope_rsmi_num_monitor_devices = (rsmi_num_monitor_devices_fn)rigscope_rsmi_symbol("rsmi_num_monitor_devices");
	rigscope_rsmi_dev_name_get = (rsmi_dev_name_get_fn)rigscope_rsmi_symbol("rsmi_dev_name_get");
	rigscope_rsmi_dev_power_get = (rsmi_dev_power_get_fn)rigscope_rsmi_symbol("rsmi_dev_power_get");
	rigscope_rsmi_dev_power_cap_get = (rsmi_dev_power_cap_get_fn)rigscope_rsmi_symbol("rsmi_dev_power_cap_get");
	rigscope_rsmi_dev_busy_percent_get = (rsmi_dev_busy_percent_get_fn)rigscope_rsmi_symbol("rsmi_dev_busy_percent_get");
	rigscope_rsmi_dev_temp_metric_get = (rsmi_dev_temp_metric_get_fn)rigscope_rsmi_symbol("rsmi_dev_temp_metric_get");
	rigscope_rsmi_dev_memory_usage_get = (rsmi_dev_memory_usage_get_fn)rigscope_rsmi_symbol("rsmi_dev_memory_usage_get");
	rigscope_rsmi_dev_memory_total_get = (rsmi_dev_memory_total_get_fn)rigscope_rsmi_symbol("rsmi_dev_memory_total_get");
	rigscope_rsmi_dev_memory_busy_percent_get = (rsmi_dev_memory_busy_percent_get_fn)rigscope_rsmi_symbol("rsmi_dev_memory_busy_percent_get");

	if (rigscope_rsmi_init == NULL ||
		rigscope_rsmi_num_monitor_devices == NULL ||
		rigscope_rsmi_dev_name_get == NULL) {
		return 0;
	}
	return 1;
}

static int rigscope_rsmi_init_once(void) {
	if (!rigscope_rsmi_open()) {
		return 0;
	}
	return rigscope_rsmi_init(0) == 0;
}

static int rigscope_rsmi_count(uint32_t *count) {
	return rigscope_rsmi_num_monitor_devices(count) == 0;
}

static int rigscope_rsmi_name(uint32_t index, char *name, size_t len) {
	return rigscope_rsmi_dev_name_get(index, name, len) == 0;
}

static int rigscope_rsmi_power(uint32_t index, uint64_t *value) {
	RSMI_POWER_TYPE power_type;
	return rigscope_rsmi_dev_power_get != NULL && rigscope_rsmi_dev_power_get(index, value, &power_type) == 0;
}

static int rigscope_rsmi_power_cap(uint32_t index, uint64_t *value) {
	return rigscope_rsmi_dev_power_cap_get != NULL && rigscope_rsmi_dev_power_cap_get(index, 0, value) == 0;
}

static int rigscope_rsmi_busy(uint32_t index, uint32_t *value) {
	return rigscope_rsmi_dev_busy_percent_get != NULL && rigscope_rsmi_dev_busy_percent_get(index, value) == 0;
}

static int rigscope_rsmi_temp(uint32_t index, uint32_t sensor_type, int64_t *value) {
	return rigscope_rsmi_dev_temp_metric_get != NULL && rigscope_rsmi_dev_temp_metric_get(index, sensor_type, 0, value) == 0;
}

static int rigscope_rsmi_memory_usage(uint32_t index, int memory_type, uint64_t *value) {
	return rigscope_rsmi_dev_memory_usage_get != NULL && rigscope_rsmi_dev_memory_usage_get(index, memory_type, value) == 0;
}

static int rigscope_rsmi_memory_total(uint32_t index, int memory_type, uint64_t *value) {
	return rigscope_rsmi_dev_memory_total_get != NULL && rigscope_rsmi_dev_memory_total_get(index, memory_type, value) == 0;
}

static int rigscope_rsmi_memory_busy(uint32_t index, uint32_t *value) {
	return rigscope_rsmi_dev_memory_busy_percent_get != NULL && rigscope_rsmi_dev_memory_busy_percent_get(index, value) == 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
)

var rocmMu sync.Mutex

func rocmAvailable() bool {
	rocmMu.Lock()
	defer rocmMu.Unlock()

	if C.rigscope_rsmi_init_once() == 0 {
		return false
	}
	var count C.uint32_t
	return C.rigscope_rsmi_count(&count) != 0 && count > 0
}

func sampleROCM(context.Context) (map[string]any, error) {
	rocmMu.Lock()
	defer rocmMu.Unlock()

	if C.rigscope_rsmi_init_once() == 0 {
		return nil, fmt.Errorf("ROCm SMI unavailable")
	}
	var count C.uint32_t
	if C.rigscope_rsmi_count(&count) == 0 {
		return nil, fmt.Errorf("ROCm SMI device count unavailable")
	}

	devices := make([]map[string]any, 0, int(count))
	for i := C.uint32_t(0); i < count; i++ {
		record := map[string]any{"index": int(i)}

		name := make([]C.char, 128)
		if C.rigscope_rsmi_name(i, &name[0], C.size_t(len(name))) != 0 {
			record["name"] = C.GoString(&name[0])
		}

		var power C.uint64_t
		if C.rigscope_rsmi_power(i, &power) != 0 {
			record["power_w"] = float64(power) / 1_000_000
		}
		if C.rigscope_rsmi_power_cap(i, &power) != 0 {
			record["power_limit_w"] = float64(power) / 1_000_000
		}

		var percent C.uint32_t
		if C.rigscope_rsmi_busy(i, &percent) != 0 {
			record["util_pct"] = float64(percent)
		}
		if C.rigscope_rsmi_memory_busy(i, &percent) != 0 {
			record["mem_util_pct"] = float64(percent)
		}

		var temp C.int64_t
		if C.rigscope_rsmi_temp(i, 0, &temp) != 0 {
			record["temp_c"] = float64(temp) / 1000
		}

		var memory C.uint64_t
		if C.rigscope_rsmi_memory_usage(i, 0, &memory) != 0 {
			record["vram_used_bytes"] = float64(memory)
		}
		if C.rigscope_rsmi_memory_total(i, 0, &memory) != 0 {
			record["vram_total_bytes"] = float64(memory)
		}
		if C.rigscope_rsmi_memory_usage(i, 2, &memory) != 0 {
			record["gtt_used_bytes"] = float64(memory)
		}
		if C.rigscope_rsmi_memory_total(i, 2, &memory) != 0 {
			record["gtt_total_bytes"] = float64(memory)
		}

		devices = append(devices, record)
	}

	return map[string]any{"devices": devices}, nil
}
