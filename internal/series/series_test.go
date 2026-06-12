package series

import "testing"

func TestFlattenSample(t *testing.T) {
	sample := map[string]any{
		"collectors": []map[string]any{
			{
				"collector": "nvidia",
				"devices": []map[string]any{
					{
						"index":           0,
						"name":            "Test GPU",
						"power_w":         321.5,
						"power_limit_w":   600.0,
						"sm_clock_mhz":    2400.0,
						"mem_clock_mhz":   1400.0,
						"temp_c":          54.0,
						"util_pct":        93.0,
						"mem_used_mib":    16384.0,
						"ignored_counter": 1.0,
					},
				},
			},
			{
				"collector":           "zenpower",
				"cpu_package_power_w": 88.25,
			},
			{
				"collector": "load",
				"metrics": []map[string]any{
					{
						"name":   "load_1",
						"value":  1.25,
						"unit":   "ratio",
						"symbol": "",
						"kind":   "load",
					},
				},
			},
		},
	}

	points := FlattenSample(sample)
	if got, want := len(points), 9; got != want {
		t.Fatalf("len(points) = %d, want %d", got, want)
	}

	seen := map[string]Point{}
	for _, point := range points {
		seen[point.Key()] = point
	}

	gpuPower := Metric{
		Name: "gpu_power_w",
		Labels: map[string]string{
			"collector": "nvidia",
			"device":    "Test GPU",
			"index":     "0",
		},
	}.Key()
	if got, want := seen[gpuPower].Value, 321.5; got != want {
		t.Fatalf("gpu_power_w = %v, want %v", got, want)
	}

	cpuPower := Metric{
		Name: "cpu_package_power_w",
		Labels: map[string]string{
			"collector": "zenpower",
		},
	}.Key()
	if got, want := seen[cpuPower].Value, 88.25; got != want {
		t.Fatalf("cpu_package_power_w = %v, want %v", got, want)
	}

	load := Metric{
		Name: "load_1",
		Labels: map[string]string{
			"collector": "load",
		},
	}.Key()
	if got, want := seen[load].Value, 1.25; got != want {
		t.Fatalf("load_1 = %v, want %v", got, want)
	}
	if got, want := seen[load].Unit, "ratio"; got != want {
		t.Fatalf("load_1 unit = %q, want %q", got, want)
	}
}

func TestFlattenROCMDeviceMemoryFields(t *testing.T) {
	points := FlattenSample(map[string]any{
		"collectors": []map[string]any{
			{
				"collector": "rocm",
				"devices": []map[string]any{
					{
						"index":            0,
						"name":             "AMD GPU",
						"mem_util_pct":     12.0,
						"vram_total_bytes": 32_000_000_000.0,
						"vram_used_bytes":  8_000_000_000.0,
						"gtt_total_bytes":  16_000_000_000.0,
						"gtt_used_bytes":   1_000_000_000.0,
					},
				},
			},
		},
	})

	seen := map[string]Point{}
	for _, point := range points {
		seen[point.Name] = point
	}
	for _, name := range []string{
		"gpu_mem_util_pct",
		"gpu_vram_total_bytes",
		"gpu_vram_used_bytes",
		"gpu_gtt_total_bytes",
		"gpu_gtt_used_bytes",
	} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing flattened metric %q: %#v", name, points)
		}
	}
	if got, want := seen["gpu_vram_used_bytes"].Unit, "byte"; got != want {
		t.Fatalf("gpu_vram_used_bytes unit = %q, want %q", got, want)
	}
}
