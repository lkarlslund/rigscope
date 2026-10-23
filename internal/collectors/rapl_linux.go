//go:build linux

package collectors

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxRAPLWatts rejects deltas from counter resets or misread files.
const maxRAPLWatts = 5000

func init() {
	Register(Registration{
		Name: "rapl",
		Detect: func() (Collector, bool, error) {
			collector := &RAPL{Root: "/sys/class/powercap"}
			ok, err := collector.detect()
			return collector, ok, err
		},
	})
}

// RAPL turns the powercap energy counters (Intel RAPL, and AMD RAPL on recent
// kernels) into power. The counters are root-only unless a udev rule makes
// energy_uj readable, so detection only succeeds when at least one zone can be
// read.
type RAPL struct {
	Root string

	mu    sync.Mutex
	zones []raplZone
	last  map[string]raplReading
}

type raplZone struct {
	id       string // intel-rapl:0:1
	domain   string // package-0, core, uncore, dram, psys
	sensor   string
	path     string
	maxRange float64
}

type raplReading struct {
	energyUJ float64
	at       time.Time
}

func (*RAPL) Name() string { return "rapl" }

func (c *RAPL) detect() (bool, error) {
	zones, err := c.findZones()
	if err != nil {
		return false, err
	}
	c.zones = zones
	return len(zones) > 0, nil
}

func (c *RAPL) findZones() ([]raplZone, error) {
	// intel-rapl:* skips intel-rapl-mmio, which mirrors the package counter.
	dirs, err := filepath.Glob(filepath.Join(c.Root, "intel-rapl:*"))
	if err != nil {
		return nil, err
	}
	slices.Sort(dirs)
	packages := 0
	for _, dir := range dirs {
		if strings.HasPrefix(readTrim(filepath.Join(dir, "name")), "package-") {
			packages++
		}
	}
	zones := []raplZone{}
	for _, dir := range dirs {
		path := filepath.Join(dir, "energy_uj")
		if _, ok := readFloatFile(path); !ok {
			continue
		}
		domain := readTrim(filepath.Join(dir, "name"))
		maxRange, _ := readFloatFile(filepath.Join(dir, "max_energy_range_uj"))
		zones = append(zones, raplZone{
			id:       filepath.Base(dir),
			domain:   domain,
			sensor:   raplSensorName(domain, packages),
			path:     path,
			maxRange: maxRange,
		})
	}
	return zones, nil
}

func raplSensorName(domain string, packages int) string {
	switch {
	case strings.HasPrefix(domain, "package-"):
		if packages > 1 {
			return "CPU package " + strings.TrimPrefix(domain, "package-")
		}
		return "CPU package"
	case domain == "core":
		return "CPU cores"
	case domain == "uncore":
		return "CPU uncore"
	case domain == "dram":
		return "DRAM"
	case domain == "psys":
		return "Platform"
	default:
		return domain
	}
}

func (c *RAPL) Sample(context.Context) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = map[string]raplReading{}
	}
	m := []map[string]any{}
	var errs []error
	for _, zone := range c.zones {
		raw := readTrim(zone.path)
		energy, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("reading %s: %q", zone.path, raw))
			continue
		}
		now := time.Now()
		prev, seen := c.last[zone.id]
		c.last[zone.id] = raplReading{energyUJ: energy, at: now}
		if !seen {
			continue
		}
		watts, ok := raplWatts(prev, raplReading{energyUJ: energy, at: now}, zone.maxRange)
		if !ok {
			continue
		}
		m = append(m, raplMetric(zone, watts))
	}
	if len(m) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return metricRecord(m), nil
}

func raplWatts(prev, cur raplReading, maxRange float64) (float64, bool) {
	seconds := cur.at.Sub(prev.at).Seconds()
	if seconds <= 0 {
		return 0, false
	}
	delta := cur.energyUJ - prev.energyUJ
	if delta < 0 {
		if maxRange <= 0 {
			return 0, false
		}
		delta += maxRange
	}
	watts := delta / 1e6 / seconds
	if watts < 0 || watts > maxRAPLWatts {
		return 0, false
	}
	return watts, true
}

func raplMetric(zone raplZone, watts float64) map[string]any {
	labels := map[string]string{
		"zone":   zone.id,
		"domain": zone.domain,
		"sensor": zone.sensor,
	}
	switch {
	case strings.HasPrefix(zone.domain, "package-"):
		return metric("cpu_package_power_w", watts, "watt", "W", "power", labels)
	case zone.domain == "dram":
		return metric("rapl_power_w", watts, "watt", "W", "power", labels)
	default:
		// Cores and uncore are part of the package, and psys covers the whole
		// platform, so they must not be added to the package total.
		return metric("rapl_power_w", watts, "watt", "W", "power_component", labels)
	}
}
