//go:build windows

package collectors

import (
	"context"
	"os"
	"strings"

	cpuutil "github.com/shirou/gopsutil/v4/cpu"
	diskutil "github.com/shirou/gopsutil/v4/disk"
	memutil "github.com/shirou/gopsutil/v4/mem"
	netutil "github.com/shirou/gopsutil/v4/net"
	processutil "github.com/shirou/gopsutil/v4/process"
)

func init() {
	Register(Registration{
		Name: "cpu",
		Detect: func() (Collector, bool, error) {
			return &WindowsCPU{}, true, nil
		},
	})
	Register(Registration{
		Name: "memory",
		Detect: func() (Collector, bool, error) {
			return WindowsMemory{}, true, nil
		},
	})
	Register(Registration{
		Name: "network",
		Detect: func() (Collector, bool, error) {
			return WindowsNetwork{}, true, nil
		},
	})
	Register(Registration{
		Name: "disk",
		Detect: func() (Collector, bool, error) {
			return WindowsDisk{}, true, nil
		},
	})
	Register(Registration{
		Name: "filesystem",
		Detect: func() (Collector, bool, error) {
			return WindowsFilesystem{}, true, nil
		},
	})
	Register(Registration{
		Name: "process",
		Detect: func() (Collector, bool, error) {
			return WindowsProcess{}, true, nil
		},
	})
	Register(Registration{
		Name: "self",
		Detect: func() (Collector, bool, error) {
			return WindowsSelf{}, true, nil
		},
	})
}

type WindowsCPU struct {
	prev *cpuutil.TimesStat
}

func (*WindowsCPU) Name() string { return "cpu" }

func (c *WindowsCPU) Sample(ctx context.Context) (map[string]any, error) {
	cores, err := cpuutil.CountsWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	times, err := cpuutil.TimesWithContext(ctx, false)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{
		metric("cpu_cores", float64(cores), "count", "", "cpu", nil),
	}
	if len(times) > 0 {
		current := times[0]
		if c.prev != nil {
			total := cpuTotal(current) - cpuTotal(*c.prev)
			if total > 0 {
				user := (current.User + current.Nice) - (c.prev.User + c.prev.Nice)
				system := current.System - c.prev.System
				idle := current.Idle - c.prev.Idle
				m = append(m,
					metric("cpu_user_pct", 100*clampNonNegative(user)/total, "percent", "%", "cpu", nil),
					metric("cpu_system_pct", 100*clampNonNegative(system)/total, "percent", "%", "cpu", nil),
					metric("cpu_idle_pct", 100*clampNonNegative(idle)/total, "percent", "%", "cpu", nil),
				)
			}
		}
		c.prev = &current
	}
	return metricRecord(m), nil
}

func cpuTotal(t cpuutil.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func clampNonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

type WindowsMemory struct{}

func (WindowsMemory) Name() string { return "memory" }

func (WindowsMemory) Sample(ctx context.Context) (map[string]any, error) {
	vm, err := memutil.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{
		metric("memory_total_bytes", float64(vm.Total), "byte", "B", "memory", nil),
		metric("memory_available_bytes", float64(vm.Available), "byte", "B", "memory", nil),
		metric("memory_used_bytes", float64(vm.Used), "byte", "B", "memory", nil),
		metric("memory_free_bytes", float64(vm.Free), "byte", "B", "memory", nil),
	}
	if swap, err := memutil.SwapMemoryWithContext(ctx); err == nil {
		m = append(m,
			metric("swap_total_bytes", float64(swap.Total), "byte", "B", "memory", nil),
			metric("swap_free_bytes", float64(swap.Free), "byte", "B", "memory", nil),
			metric("swap_used_bytes", float64(swap.Used), "byte", "B", "memory", nil),
		)
	}
	return metricRecord(m), nil
}

type WindowsNetwork struct{}

func (WindowsNetwork) Name() string { return "network" }

func (WindowsNetwork) Sample(ctx context.Context) (map[string]any, error) {
	stats, err := netutil.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, stat := range stats {
		if stat.Name == "" || strings.EqualFold(stat.Name, "Loopback Pseudo-Interface 1") {
			continue
		}
		labels := map[string]string{"interface": stat.Name}
		m = append(m,
			metric("network_rx_bytes_total", float64(stat.BytesRecv), "byte", "B", "counter", labels),
			metric("network_tx_bytes_total", float64(stat.BytesSent), "byte", "B", "counter", labels),
			metric("network_rx_packets_total", float64(stat.PacketsRecv), "count", "", "counter", labels),
			metric("network_tx_packets_total", float64(stat.PacketsSent), "count", "", "counter", labels),
			metric("network_rx_errors_total", float64(stat.Errin), "count", "", "counter", labels),
			metric("network_tx_errors_total", float64(stat.Errout), "count", "", "counter", labels),
			metric("network_rx_drops_total", float64(stat.Dropin), "count", "", "counter", labels),
			metric("network_tx_drops_total", float64(stat.Dropout), "count", "", "counter", labels),
		)
	}
	return metricRecord(m), nil
}

type WindowsDisk struct{}

func (WindowsDisk) Name() string { return "disk" }

func (WindowsDisk) Sample(ctx context.Context) (map[string]any, error) {
	stats, err := diskutil.IOCountersWithContext(ctx)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for device, stat := range stats {
		if device == "" {
			device = stat.Name
		}
		if device == "" {
			continue
		}
		labels := map[string]string{"device": device}
		m = append(m,
			metric("disk_reads_total", float64(stat.ReadCount), "count", "", "counter", labels),
			metric("disk_writes_total", float64(stat.WriteCount), "count", "", "counter", labels),
			metric("disk_read_bytes_total", float64(stat.ReadBytes), "byte", "B", "counter", labels),
			metric("disk_written_bytes_total", float64(stat.WriteBytes), "byte", "B", "counter", labels),
		)
	}
	return metricRecord(m), nil
}

type WindowsFilesystem struct{}

func (WindowsFilesystem) Name() string { return "filesystem" }

func (WindowsFilesystem) Sample(ctx context.Context) (map[string]any, error) {
	partitions, err := diskutil.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for _, partition := range partitions {
		if partition.Mountpoint == "" {
			continue
		}
		usage, err := diskutil.UsageWithContext(ctx, partition.Mountpoint)
		if err != nil {
			continue
		}
		labels := map[string]string{
			"mount":  partition.Mountpoint,
			"fstype": partition.Fstype,
		}
		m = append(m,
			metric("filesystem_total_bytes", float64(usage.Total), "byte", "B", "filesystem", labels),
			metric("filesystem_free_bytes", float64(usage.Free), "byte", "B", "filesystem", labels),
			metric("filesystem_used_bytes", float64(usage.Used), "byte", "B", "filesystem", labels),
		)
	}
	return metricRecord(m), nil
}

type WindowsProcess struct{}

func (WindowsProcess) Name() string { return "process" }

func (WindowsProcess) Sample(ctx context.Context) (map[string]any, error) {
	pids, err := processutil.PidsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	// Per-process status/thread queries are too expensive for short sampling
	// intervals on Windows. Report the fast process count as running and keep
	// Linux-only state buckets present for dashboard compatibility.
	running := float64(len(pids))
	return metricRecord([]map[string]any{
		metric("process_running", running, "count", "", "process", nil),
		metric("process_sleeping", 0, "count", "", "process", nil),
		metric("process_blocked", 0, "count", "", "process", nil),
		metric("process_zombie", 0, "count", "", "process", nil),
		metric("process_threads", 0, "count", "", "process", nil),
	}), nil
}

type WindowsSelf struct{}

func (WindowsSelf) Name() string { return "self" }

func (WindowsSelf) Sample(ctx context.Context) (map[string]any, error) {
	proc, err := processutil.NewProcessWithContext(ctx, int32(os.Getpid()))
	if err != nil {
		return nil, err
	}
	memInfo, err := proc.MemoryInfoWithContext(ctx)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{
		metric("self_rss_bytes", float64(memInfo.RSS), "byte", "B", "memory", nil),
	}
	if handles, err := proc.NumFDsWithContext(ctx); err == nil {
		m = append(m,
			metric("self_open_fds", float64(handles), "count", "", "process", nil),
			metric("self_handles", float64(handles), "count", "", "process", nil),
		)
	}
	return metricRecord(m), nil
}
