package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/lkarlslund/rigscope/internal/collectors"
	"github.com/lkarlslund/rigscope/internal/series"
	"github.com/lkarlslund/rigscope/internal/store"
)

type Monitor struct {
	Collectors    []collectors.Collector
	Store         *store.Store
	Interval      time.Duration
	SampleTimeout time.Duration
	Log           *slog.Logger
	OnSample      func(time.Time, []series.Point, []map[string]string)

	counterRates series.CounterRateTransformer
	sampler      collectors.Sampler
}

func (m *Monitor) Run(ctx context.Context) error {
	if m.Interval <= 0 {
		m.Interval = time.Second
	}
	if m.SampleTimeout <= 0 {
		m.SampleTimeout = m.Interval * 8 / 10
	}
	m.sampler.Timeout = m.SampleTimeout
	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()

	for {
		m.sample(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Monitor) sample(ctx context.Context) {
	sample := m.sampler.SampleAll(ctx, m.Collectors)
	collectorErrors := collectorErrors(sample)
	points := series.FlattenSample(sample)
	timestamp := time.Now()
	points = m.counterRates.Transform(timestamp, points)
	if err := m.Store.Insert(timestamp, points); err != nil {
		m.logger().Warn("insert sample", "error", err)
	}
	if m.OnSample != nil {
		m.OnSample(timestamp, points, collectorErrors)
	}
}

func collectorErrors(sample map[string]any) []map[string]string {
	rawRecords, ok := sample["collectors"].([]map[string]any)
	if !ok {
		return nil
	}
	out := []map[string]string{}
	for _, record := range rawRecords {
		errText, ok := record["error"].(string)
		if !ok || errText == "" {
			continue
		}
		name, _ := record["collector"].(string)
		if name == "" {
			name = "unknown"
		}
		out = append(out, map[string]string{"collector": name, "error": errText})
	}
	return out
}

func (m *Monitor) logger() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
