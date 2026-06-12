package collectors

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredCollectorsIncludesCoreSubsystems(t *testing.T) {
	seen := map[string]bool{}
	for _, reg := range Registered() {
		seen[reg.Name] = true
	}
	for _, name := range []string{
		"cpu",
		"disk",
		"drm",
		"filesystem",
		"load",
		"memory",
		"network",
		"nvidia",
		"power_supply",
		"process",
		"rocm",
		"self",
		"socket",
		"thermal",
		"xdna",
		"zenpower",
	} {
		if !seen[name] {
			t.Fatalf("collector %q not registered", name)
		}
	}
}

func TestMetricDefaultSymbols(t *testing.T) {
	tests := []struct {
		unit string
		want string
	}{
		{unit: "count", want: "count"},
		{unit: "ratio", want: "1"},
		{unit: "second", want: "s"},
		{unit: "byte", want: ""},
	}
	for _, tt := range tests {
		if got := defaultSymbol(tt.unit); got != tt.want {
			t.Fatalf("defaultSymbol(%q) = %q, want %q", tt.unit, got, tt.want)
		}
	}
}

func TestPreferLowOverheadCollectorsSkipsROCMWhenDRMSeesAMD(t *testing.T) {
	root := t.TempDir()
	device := filepath.Join(root, "card1", "device")
	if err := os.MkdirAll(device, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "amdgpu"), filepath.Join(device, "driver")); err != nil {
		t.Fatal(err)
	}

	got := preferLowOverheadCollectors([]Collector{
		NVIDIA{},
		ROCM{},
		DRM{Root: root},
	})

	names := map[string]bool{}
	for _, collector := range got {
		names[collector.Name()] = true
	}
	if names["rocm"] {
		t.Fatalf("rocm should be skipped when DRM can monitor an AMD GPU: %#v", got)
	}
	if !names["nvidia"] || !names["drm"] {
		t.Fatalf("expected nvidia and drm collectors to remain: %#v", got)
	}
}

func TestPreferLowOverheadCollectorsKeepsROCMWithoutAMDFromDRM(t *testing.T) {
	got := preferLowOverheadCollectors([]Collector{
		ROCM{},
		DRM{Root: t.TempDir()},
	})

	names := map[string]bool{}
	for _, collector := range got {
		names[collector.Name()] = true
	}
	if !names["rocm"] {
		t.Fatalf("rocm should remain when DRM does not expose an AMD GPU: %#v", got)
	}
}

func TestThermalHwmonMetricsIncludesGenericPowerSensors(t *testing.T) {
	root := t.TempDir()
	nvme := filepath.Join(root, "hwmon0")
	amdgpu := filepath.Join(root, "hwmon1")
	if err := os.MkdirAll(nvme, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(amdgpu, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nvme, "name"), "nvme\n")
	writeFile(t, filepath.Join(nvme, "power1_input"), "2500000\n")
	writeFile(t, filepath.Join(nvme, "power1_label"), "controller\n")
	writeFile(t, filepath.Join(amdgpu, "name"), "amdgpu\n")
	writeFile(t, filepath.Join(amdgpu, "power1_input"), "50000000\n")

	metrics, err := (Thermal{HwmonRoot: root}).hwmonMetrics()
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, metric := range metrics {
		if metric["name"] != "hwmon_power_w" {
			continue
		}
		labels := metric["labels"].(map[string]string)
		if labels["chip"] == "amdgpu" {
			t.Fatal("amdgpu hwmon power should be left to the DRM collector")
		}
		if labels["chip"] == "nvme" && labels["sensor"] == "controller" && metric["value"] == 2.5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing nvme hwmon power metric: %#v", metrics)
	}
}

func TestSocketCollectorReadsSockstatAndTCPStates(t *testing.T) {
	root := t.TempDir()
	sockstat := filepath.Join(root, "sockstat")
	tcp := filepath.Join(root, "tcp")
	writeFile(t, sockstat, strings.Join([]string{
		"sockets: used 16",
		"TCP: inuse 3 orphan 1 tw 2 alloc 4 mem 5",
		"UDP: inuse 6 mem 7",
		"RAW: inuse 1",
		"FRAG: inuse 2 memory 0",
	}, "\n"))
	writeFile(t, tcp, strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		"   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 1 1 0000000000000000 100 0 0 10 0",
		"   1: 0100007F:1F91 0100007F:0050 01 00000000:00000000 00:00000000 00000000 1000 0 2 1 0000000000000000 100 0 0 10 0",
		"   2: 0100007F:1F92 0100007F:0050 06 00000000:00000000 00:00000000 00000000 1000 0 3 1 0000000000000000 100 0 0 10 0",
	}, "\n"))

	sample, err := (Socket{SockstatPaths: []string{sockstat}, TCPPaths: []string{tcp}}).Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	metrics := sample["metrics"].([]map[string]any)
	seen := map[string]float64{}
	for _, metric := range metrics {
		seen[metric["name"].(string)] = metric["value"].(float64)
	}
	for name, want := range map[string]float64{
		"sockets_used":                16,
		"tcp_sockets_in_use":          3,
		"tcp_sockets_time_wait":       2,
		"udp_sockets_in_use":          6,
		"raw_sockets_in_use":          1,
		"fragment_queues_in_use":      2,
		"tcp_connections_listen":      1,
		"tcp_connections_established": 1,
		"tcp_connections_time_wait":   1,
	} {
		if got := seen[name]; got != want {
			t.Fatalf("%s = %v, want %v in %#v", name, got, want, seen)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStatusValue(t *testing.T) {
	tests := []struct {
		status string
		want   float64
		ok     bool
	}{
		{status: "active", want: 1, ok: true},
		{status: "suspended", want: 0, ok: true},
		{status: "suspending", want: 0, ok: true},
		{status: "unknown", ok: false},
	}
	for _, tt := range tests {
		got, ok := runtimeStatusValue(tt.status)
		if ok != tt.ok {
			t.Fatalf("runtimeStatusValue(%q) ok = %v, want %v", tt.status, ok, tt.ok)
		}
		if got != tt.want {
			t.Fatalf("runtimeStatusValue(%q) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestPCIBDFFallback(t *testing.T) {
	if got, want := pciBDF("/does/not/exist/device"), "device"; got != want {
		t.Fatalf("pciBDF() = %q, want %q", got, want)
	}
}
