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
	Collectors []collectors.Collector
	Store      *store.Store
	Interval   time.Duration
	Log        *slog.Logger
	OnSample   func(time.Time, []series.Point)

	counterRates series.CounterRateTransformer
}

func (m *Monitor) Run(ctx context.Context) error {
	if m.Interval <= 0 {
		m.Interval = time.Second
	}
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
	sample := collectors.SampleAll(ctx, m.Collectors)
	points := series.FlattenSample(sample)
	timestamp := time.Now()
	points = m.counterRates.Transform(timestamp, points)
	if err := m.Store.Insert(timestamp, points); err != nil {
		m.logger().Warn("insert sample", "error", err)
	}
	if m.OnSample != nil {
		m.OnSample(timestamp, points)
	}
}

func (m *Monitor) logger() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
