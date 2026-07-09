//go:build linux

package collectors

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	registerFileCollector("load", "/proc/loadavg", Load{Path: "/proc/loadavg"})
	registerFileCollector("memory", "/proc/meminfo", Memory{Path: "/proc/meminfo"})
	registerFileCollector("cpu", "/proc/stat", &CPU{Path: "/proc/stat"})
	registerFileCollector("network", "/proc/net/dev", &Network{Path: "/proc/net/dev"})
	registerFileCollector("socket", "/proc/net/sockstat", Socket{
		SockstatPaths: []string{"/proc/net/sockstat", "/proc/net/sockstat6"},
		TCPPaths:      []string{"/proc/net/tcp", "/proc/net/tcp6"},
	})
	registerFileCollector("disk", "/proc/diskstats", &Disk{Path: "/proc/diskstats"})
	registerFileCollector("process", "/proc", Process{ProcRoot: "/proc"})
	registerFileCollector("self", "/proc/self/status", Self{ProcRoot: "/proc"})
	Register(Registration{
		Name: "filesystem",
		Detect: func() (Collector, bool, error) {
			if _, err := os.Stat("/proc/self/mounts"); err != nil {
				return nil, false, nil
			}
			return Filesystem{MountsPath: "/proc/self/mounts"}, true, nil
		},
	})
}

func registerFileCollector(name string, path string, collector Collector) {
	Register(Registration{
		Name: name,
		Detect: func() (Collector, bool, error) {
			if _, err := os.Stat(path); err != nil {
				return nil, false, nil
			}
			return collector, true, nil
		},
	})
}

type Load struct {
	Path string
}

func (Load) Name() string { return "load" }

func (c Load) Sample(context.Context) (map[string]any, error) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, nil
	}
	m := []map[string]any{}
	for _, item := range []struct {
		name string
		raw  string
	}{
		{name: "load_1", raw: fields[0]},
		{name: "load_5", raw: fields[1]},
		{name: "load_15", raw: fields[2]},
	} {
		if value, err := strconv.ParseFloat(item.raw, 64); err == nil {
			m = append(m, metric(item.name, value, "ratio", "", "load", nil))
		}
	}
	return metricRecord(m), nil
}

type Memory struct {
	Path string
}

func (Memory) Name() string { return "memory" }

func (c Memory) Sample(context.Context) (map[string]any, error) {
	values, err := readMeminfo(c.Path)
	if err != nil {
		return nil, err
	}
	total := values["MemTotal"]
	free := values["MemFree"]
	available := values["MemAvailable"]
	buffers := values["Buffers"]
	cached := values["Cached"]
	shmem := values["Shmem"]
	swapTotal := values["SwapTotal"]
	swapFree := values["SwapFree"]
	used := total - available
	swapUsed := swapTotal - swapFree
	m := []map[string]any{
		metric("memory_total_bytes", total, "byte", "B", "memory", nil),
		metric("memory_free_bytes", free, "byte", "B", "memory", nil),
		metric("memory_available_bytes", available, "byte", "B", "memory", nil),
		metric("memory_used_bytes", used, "byte", "B", "memory", nil),
		metric("memory_buffers_bytes", buffers, "byte", "B", "memory", nil),
		metric("memory_cached_bytes", cached, "byte", "B", "memory", nil),
		metric("memory_shmem_bytes", shmem, "byte", "B", "memory", nil),
		metric("swap_total_bytes", swapTotal, "byte", "B", "memory", nil),
		metric("swap_free_bytes", swapFree, "byte", "B", "memory", nil),
		metric("swap_used_bytes", swapUsed, "byte", "B", "memory", nil),
	}
	return metricRecord(m), nil
}

func readMeminfo(path string) (map[string]float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	values := map[string]float64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if len(fields) >= 3 && fields[2] == "kB" {
			value *= 1024
		}
		values[key] = value
	}
	return values, scanner.Err()
}

type CPU struct {
	Path string
	prev cpuTimes
}

func (*CPU) Name() string { return "cpu" }

type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c *CPU) Sample(context.Context) (map[string]any, error) {
	current, cores, err := readCPUTimes(c.Path)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{
		metric("cpu_cores", float64(cores), "count", "", "cpu", nil),
	}
	if c.prev.total() > 0 {
		delta := current.sub(c.prev)
		total := float64(delta.total())
		if total > 0 {
			m = append(m,
				metric("cpu_user_pct", 100*float64(delta.user+delta.nice)/total, "percent", "%", "cpu", nil),
				metric("cpu_system_pct", 100*float64(delta.system)/total, "percent", "%", "cpu", nil),
				metric("cpu_iowait_pct", 100*float64(delta.iowait)/total, "percent", "%", "cpu", nil),
				metric("cpu_irq_pct", 100*float64(delta.irq+delta.softirq)/total, "percent", "%", "cpu", nil),
				metric("cpu_steal_pct", 100*float64(delta.steal)/total, "percent", "%", "cpu", nil),
				metric("cpu_idle_pct", 100*float64(delta.idle)/total, "percent", "%", "cpu", nil),
			)
		}
	}
	c.prev = current
	return metricRecord(m), nil
}

func (t cpuTimes) total() uint64 {
	return t.user + t.nice + t.system + t.idle + t.iowait + t.irq + t.softirq + t.steal
}

func (t cpuTimes) sub(prev cpuTimes) cpuTimes {
	return cpuTimes{
		user:    subUint(t.user, prev.user),
		nice:    subUint(t.nice, prev.nice),
		system:  subUint(t.system, prev.system),
		idle:    subUint(t.idle, prev.idle),
		iowait:  subUint(t.iowait, prev.iowait),
		irq:     subUint(t.irq, prev.irq),
		softirq: subUint(t.softirq, prev.softirq),
		steal:   subUint(t.steal, prev.steal),
	}
}

func readCPUTimes(path string) (cpuTimes, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return cpuTimes{}, 0, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	var times cpuTimes
	cores := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "cpu" && len(fields) >= 8 {
			times = cpuTimes{
				user:    parseUint(fields[1]),
				nice:    parseUint(fields[2]),
				system:  parseUint(fields[3]),
				idle:    parseUint(fields[4]),
				iowait:  parseUint(fields[5]),
				irq:     parseUint(fields[6]),
				softirq: parseUint(fields[7]),
			}
			if len(fields) > 8 {
				times.steal = parseUint(fields[8])
			}
			continue
		}
		if strings.HasPrefix(fields[0], "cpu") && len(fields[0]) > 3 && fields[0][3] >= '0' && fields[0][3] <= '9' {
			cores++
		}
	}
	return times, cores, scanner.Err()
}

type Network struct {
	Path string
	prev map[string]netCounters
}

func (*Network) Name() string { return "network" }

type netCounters struct {
	rxBytes, rxPackets, rxErrors, rxDrops uint64
	txBytes, txPackets, txErrors, txDrops uint64
}

func (c *Network) Sample(context.Context) (map[string]any, error) {
	current, err := readNetDev(c.Path)
	if err != nil {
		return nil, err
	}
	m := []map[string]any{}
	for iface, counters := range current {
		labels := map[string]string{"interface": iface}
		m = append(m,
			metric("network_rx_bytes_total", float64(counters.rxBytes), "byte", "B", "counter", labels),
			metric("network_tx_bytes_total", float64(counters.txBytes), "byte", "B", "counter", labels),
			metric("network_rx_packets_total", float64(counters.rxPackets), "count", "", "counter", labels),
			metric("network_tx_packets_total", float64(counters.txPackets), "count", "", "counter", labels),
			metric("network_rx_errors_total", float64(counters.rxErrors), "count", "", "counter", labels),
			metric("network_tx_errors_total", float64(counters.txErrors), "count", "", "counter", labels),
			metric("network_rx_drops_total", float64(counters.rxDrops), "count", "", "counter", labels),
			metric("network_tx_drops_total", float64(counters.txDrops), "count", "", "counter", labels),
		)
	}
	c.prev = current
	return metricRecord(m), nil
}

func readNetDev(path string) (map[string]netCounters, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	out := map[string]netCounters{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 16 || iface == "lo" {
			continue
		}
		out[iface] = netCounters{
			rxBytes:   parseUint(fields[0]),
			rxPackets: parseUint(fields[1]),
			rxErrors:  parseUint(fields[2]),
			rxDrops:   parseUint(fields[3]),
			txBytes:   parseUint(fields[8]),
			txPackets: parseUint(fields[9]),
			txErrors:  parseUint(fields[10]),
			txDrops:   parseUint(fields[11]),
		}
	}
	return out, scanner.Err()
}

type Socket struct {
	SockstatPaths []string
	TCPPaths      []string
}

func (Socket) Name() string { return "socket" }

func (c Socket) Sample(context.Context) (map[string]any, error) {
	m := []map[string]any{}
	for _, path := range c.SockstatPaths {
		values, err := readSockstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for name, value := range values {
			m = append(m, metric(name, float64(value), "count", "", "socket", nil))
		}
	}
	states := map[string]uint64{}
	for _, path := range c.TCPPaths {
		values, err := readTCPStates(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for state, value := range values {
			states[state] += value
		}
	}
	for state, value := range states {
		m = append(m, metric("tcp_connections_"+state, float64(value), "count", "", "connection", nil))
	}
	return metricRecord(m), nil
}

func readSockstat(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	out := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		group := strings.TrimSuffix(strings.ToLower(fields[0]), ":")
		for i := 1; i+1 < len(fields); i += 2 {
			key := strings.ToLower(fields[i])
			value := parseUint(fields[i+1])
			switch group {
			case "sockets":
				if key == "used" {
					out["sockets_used"] += value
				}
			case "tcp":
				switch key {
				case "inuse":
					out["tcp_sockets_in_use"] += value
				case "tw":
					out["tcp_sockets_time_wait"] += value
				case "orphan":
					out["tcp_sockets_orphaned"] += value
				}
			case "udp":
				if key == "inuse" {
					out["udp_sockets_in_use"] += value
				}
			case "raw":
				if key == "inuse" {
					out["raw_sockets_in_use"] += value
				}
			case "frag":
				if key == "inuse" {
					out["fragment_queues_in_use"] += value
				}
			}
		}
	}
	return out, scanner.Err()
}

func readTCPStates(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	out := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] == "sl" {
			continue
		}
		if state := tcpStateName(fields[3]); state != "" {
			out[state]++
		}
	}
	return out, scanner.Err()
}

func tcpStateName(hexState string) string {
	switch strings.ToUpper(hexState) {
	case "01":
		return "established"
	case "02":
		return "syn_sent"
	case "03":
		return "syn_recv"
	case "04":
		return "fin_wait1"
	case "05":
		return "fin_wait2"
	case "06":
		return "time_wait"
	case "07":
		return "close"
	case "08":
		return "close_wait"
	case "09":
		return "last_ack"
	case "0A":
		return "listen"
	case "0B":
		return "closing"
	default:
		return ""
	}
}

type Disk struct {
	Path string
}

func (*Disk) Name() string { return "disk" }

func (c *Disk) Sample(context.Context) (map[string]any, error) {
	file, err := os.Open(c.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	m := []map[string]any{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}
		device := fields[2]
		if skipBlockDevice(device) {
			continue
		}
		labels := map[string]string{"device": device}
		sectorsRead := parseUint(fields[5])
		sectorsWritten := parseUint(fields[9])
		m = append(m,
			metric("disk_reads_total", float64(parseUint(fields[3])), "count", "", "counter", labels),
			metric("disk_writes_total", float64(parseUint(fields[7])), "count", "", "counter", labels),
			metric("disk_read_bytes_total", float64(sectorsRead*512), "byte", "B", "counter", labels),
			metric("disk_written_bytes_total", float64(sectorsWritten*512), "byte", "B", "counter", labels),
		)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return metricRecord(m), nil
}

func skipBlockDevice(device string) bool {
	return strings.HasPrefix(device, "loop") ||
		strings.HasPrefix(device, "ram") ||
		strings.HasPrefix(device, "zram") ||
		strings.HasPrefix(device, "dm-") ||
		isNVMeControllerNamespace(device)
}

func isNVMeControllerNamespace(device string) bool {
	if !strings.HasPrefix(device, "nvme") {
		return false
	}
	rest := strings.TrimPrefix(device, "nvme")
	controller, rest, ok := consumeDigits(rest)
	if !ok || rest == "" || rest[0] != 'c' {
		return false
	}
	rest = rest[1:]
	controllerNamespace, rest, ok := consumeDigits(rest)
	if !ok || rest == "" || rest[0] != 'n' {
		return false
	}
	rest = rest[1:]
	namespace, rest, ok := consumeDigits(rest)
	return ok && rest == "" && controller != "" && controllerNamespace != "" && namespace != ""
}

func consumeDigits(s string) (string, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return "", s, false
	}
	return s[:i], s[i:], true
}

type Filesystem struct {
	MountsPath string
}

func (Filesystem) Name() string { return "filesystem" }

func (c Filesystem) Sample(context.Context) (map[string]any, error) {
	file, err := os.Open(c.MountsPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	m := []map[string]any{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		mount := fields[1]
		fsType := fields[2]
		if seen[mount] || skipFilesystem(fsType, mount) {
			continue
		}
		seen[mount] = true
		total, free, ok := filesystemUsage(mount)
		if !ok {
			continue
		}
		used := total - free
		labels := map[string]string{"mount": mount, "fstype": fsType}
		m = append(m,
			metric("filesystem_total_bytes", total, "byte", "B", "filesystem", labels),
			metric("filesystem_free_bytes", free, "byte", "B", "filesystem", labels),
			metric("filesystem_used_bytes", used, "byte", "B", "filesystem", labels),
		)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return metricRecord(m), nil
}

func skipFilesystem(fsType, mount string) bool {
	switch fsType {
	case "proc", "sysfs", "devtmpfs", "devpts", "tmpfs", "cgroup", "cgroup2", "securityfs", "pstore", "bpf", "tracefs", "debugfs", "configfs", "fusectl", "autofs", "mqueue", "hugetlbfs":
		return true
	}
	return strings.HasPrefix(mount, "/proc") || strings.HasPrefix(mount, "/sys") || strings.HasPrefix(mount, "/dev")
}

type Process struct {
	ProcRoot string
}

func (Process) Name() string { return "process" }

func (c Process) Sample(context.Context) (map[string]any, error) {
	entries, err := os.ReadDir(c.ProcRoot)
	if err != nil {
		return nil, err
	}
	var running, sleeping, blocked, zombie, threads float64
	for _, entry := range entries {
		if !entry.IsDir() || !isPID(entry.Name()) {
			continue
		}
		state, threadCount, ok := readProcStat(filepath.Join(c.ProcRoot, entry.Name(), "stat"))
		if !ok {
			continue
		}
		threads += float64(threadCount)
		switch state {
		case "R":
			running++
		case "S", "I":
			sleeping++
		case "D":
			blocked++
		case "Z":
			zombie++
		}
	}
	return metricRecord([]map[string]any{
		metric("process_running", running, "count", "", "process", nil),
		metric("process_sleeping", sleeping, "count", "", "process", nil),
		metric("process_blocked", blocked, "count", "", "process", nil),
		metric("process_zombie", zombie, "count", "", "process", nil),
		metric("process_threads", threads, "count", "", "process", nil),
	}), nil
}

func readProcStat(path string) (string, int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, false
	}
	line := string(data)
	end := strings.LastIndexByte(line, ')')
	if end < 0 || len(line) <= end+2 {
		return "", 0, false
	}
	fields := strings.Fields(line[end+2:])
	if len(fields) < 18 {
		return "", 0, false
	}
	threads, _ := strconv.Atoi(fields[17])
	return fields[0], threads, true
}

type Self struct {
	ProcRoot string
}

func (Self) Name() string { return "self" }

func (c Self) Sample(context.Context) (map[string]any, error) {
	statusPath := filepath.Join(c.ProcRoot, "self", "status")
	values, err := readStatusValues(statusPath)
	if err != nil {
		return nil, err
	}
	fdCount := 0
	if entries, err := os.ReadDir(filepath.Join(c.ProcRoot, "self", "fd")); err == nil {
		fdCount = len(entries)
	}
	return metricRecord([]map[string]any{
		metric("self_rss_bytes", values["VmRSS"], "byte", "B", "memory", nil),
		metric("self_open_fds", float64(fdCount), "count", "", "process", nil),
	}), nil
}

func readStatusValues(path string) (map[string]float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	values := map[string]float64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		if len(fields) >= 3 && fields[2] == "kB" {
			value *= 1024
		}
		values[strings.TrimSuffix(fields[0], ":")] = value
	}
	return values, scanner.Err()
}

func parseUint(raw string) uint64 {
	value, _ := strconv.ParseUint(raw, 10, 64)
	return value
}

func subUint(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func isPID(name string) bool {
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return name != ""
}
