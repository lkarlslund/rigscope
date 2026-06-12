package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Collector interface {
	Name() string
	Sample(ctx context.Context) (map[string]any, error)
}

type Detector func() (Collector, bool, error)

type Registration struct {
	Name   string
	Detect Detector
}

var (
	registryMu sync.RWMutex
	registry   []Registration
)

func Register(reg Registration) {
	if reg.Name == "" || reg.Detect == nil {
		panic("collector registration requires name and detector")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, reg)
}

func Registered() []Registration {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Registration, len(registry))
	copy(out, registry)
	return out
}

type DefaultOptions struct {
	NVIDIA   bool
	ROCM     bool
	Zenpower bool
}

func Default(opts DefaultOptions) ([]Collector, error) {
	disabled := map[string]bool{
		"nvidia":   !opts.NVIDIA,
		"rocm":     !opts.ROCM,
		"zenpower": !opts.Zenpower,
	}
	detected := []Collector{}
	for _, reg := range Registered() {
		if disabled[reg.Name] {
			continue
		}
		collector, ok, err := reg.Detect()
		if err != nil {
			return nil, err
		}
		if ok {
			detected = append(detected, collector)
		}
	}
	return preferLowOverheadCollectors(detected), nil
}

func preferLowOverheadCollectors(cs []Collector) []Collector {
	if !hasAMDGPUFromDRM(cs) {
		return cs
	}
	out := make([]Collector, 0, len(cs))
	for _, collector := range cs {
		if collector.Name() == "rocm" {
			continue
		}
		out = append(out, collector)
	}
	return out
}

func hasAMDGPUFromDRM(cs []Collector) bool {
	for _, collector := range cs {
		drm, ok := collector.(DRM)
		if !ok {
			continue
		}
		if drm.hasDriver("amdgpu") {
			return true
		}
	}
	return false
}

func SampleAll(ctx context.Context, cs []Collector) map[string]any {
	sample := map[string]any{
		"t_wall":      float64(time.Now().UnixNano()) / 1e9,
		"t_unix_nano": time.Now().UnixNano(),
		"collectors":  []map[string]any{},
	}
	records := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		record, err := c.Sample(ctx)
		if err != nil {
			record = map[string]any{"collector": c.Name(), "error": err.Error()}
		}
		if _, ok := record["collector"]; !ok {
			record["collector"] = c.Name()
		}
		records = append(records, record)
	}
	sample["collectors"] = records
	return sample
}

type Sampler struct {
	Timeout time.Duration

	mu     sync.Mutex
	states map[string]*collectorState
}

type collectorState struct {
	inFlight bool
}

type collectorResult struct {
	index  int
	record map[string]any
}

func (s *Sampler) SampleAll(ctx context.Context, cs []Collector) map[string]any {
	sample := map[string]any{
		"t_wall":      float64(time.Now().UnixNano()) / 1e9,
		"t_unix_nano": time.Now().UnixNano(),
		"collectors":  []map[string]any{},
	}
	records := make([]map[string]any, len(cs))
	results := make(chan collectorResult, len(cs))
	pending := 0
	for i, c := range cs {
		key := collectorKey(i, c)
		if !s.tryStart(key) {
			records[i] = collectorError(c.Name(), "previous sample still running")
			continue
		}
		pending++
		go s.sampleCollector(ctx, key, i, c, results)
	}

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for pending > 0 {
		select {
		case result := <-results:
			records[result.index] = result.record
			pending--
		case <-timer.C:
			for i, c := range cs {
				if records[i] == nil {
					records[i] = collectorError(c.Name(), fmt.Sprintf("sample timed out after %s", timeout))
				}
			}
			pending = 0
		case <-ctx.Done():
			for i, c := range cs {
				if records[i] == nil {
					records[i] = collectorError(c.Name(), ctx.Err().Error())
				}
			}
			pending = 0
		}
	}
	sample["collectors"] = records
	return sample
}

func (s *Sampler) sampleCollector(ctx context.Context, key string, index int, c Collector, results chan<- collectorResult) {
	defer s.finish(key)
	record, err := c.Sample(ctx)
	if err != nil {
		record = collectorError(c.Name(), err.Error())
	}
	if _, ok := record["collector"]; !ok {
		record["collector"] = c.Name()
	}
	select {
	case results <- collectorResult{index: index, record: record}:
	case <-ctx.Done():
	}
}

func (s *Sampler) tryStart(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]*collectorState{}
	}
	state := s.states[key]
	if state == nil {
		state = &collectorState{}
		s.states[key] = state
	}
	if state.inFlight {
		return false
	}
	state.inFlight = true
	return true
}

func (s *Sampler) finish(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state := s.states[key]; state != nil {
		state.inFlight = false
	}
}

func collectorKey(index int, c Collector) string {
	return fmt.Sprintf("%d:%s", index, c.Name())
}

func collectorError(name, message string) map[string]any {
	return map[string]any{"collector": name, "error": message}
}

type NVIDIA struct{}

func init() {
	Register(Registration{
		Name: "nvidia",
		Detect: func() (Collector, bool, error) {
			return NVIDIA{}, nvidiaAvailable(), nil
		},
	})
	Register(Registration{
		Name: "rocm",
		Detect: func() (Collector, bool, error) {
			return ROCM{}, rocmAvailable(), nil
		},
	})
	Register(Registration{
		Name: "zenpower",
		Detect: func() (Collector, bool, error) {
			path, err := FindZenpower()
			if err == nil {
				return Zenpower{Path: path}, true, nil
			}
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, err
		},
	})
}

func (NVIDIA) Name() string { return "nvidia" }

func (NVIDIA) Sample(ctx context.Context) (map[string]any, error) {
	return sampleNVIDIA(ctx)
}

type ROCM struct{}

func (ROCM) Name() string { return "rocm" }

func (ROCM) Sample(ctx context.Context) (map[string]any, error) {
	return sampleROCM(ctx)
}

type Zenpower struct {
	Path string
}

func (Zenpower) Name() string { return "zenpower" }

func (z Zenpower) Sample(context.Context) (map[string]any, error) {
	data, err := os.ReadFile(z.Path)
	if err != nil {
		return nil, err
	}
	microwatts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"cpu_package_power_w": float64(microwatts) / 1_000_000,
		"path":                z.Path,
	}, nil
}

func FindZenpower() (string, error) {
	matches, err := filepath.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		return "", err
	}
	for _, dir := range matches {
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		path := filepath.Join(dir, "power1_input")
		if strings.TrimSpace(string(name)) == "zenpower" {
			if _, err := os.Stat(path); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func MarshalSample(sample map[string]any) ([]byte, error) {
	return json.Marshal(sample)
}
