package store

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/nakabonne/tstorage"

	"github.com/lkarlslund/rigscope/internal/series"
)

type Store struct {
	ts      tstorage.Storage
	mu      sync.RWMutex
	metrics map[string]series.Metric
}

func Open(path string, retention time.Duration) (*Store, error) {
	return open(path, retention)
}

func OpenInMemory(retention time.Duration) (*Store, error) {
	return open("", retention)
}

func open(path string, retention time.Duration) (*Store, error) {
	opts := []tstorage.Option{
		tstorage.WithTimestampPrecision(tstorage.Milliseconds),
		tstorage.WithPartitionDuration(time.Hour),
	}
	if path != "" {
		opts = append(opts, tstorage.WithDataPath(path))
	}
	if retention > 0 {
		opts = append(opts, tstorage.WithRetention(retention))
	}
	ts, err := tstorage.NewStorage(opts...)
	if err != nil {
		return nil, err
	}
	return &Store{
		ts:      ts,
		metrics: map[string]series.Metric{},
	}, nil
}

func (s *Store) Close() error {
	return s.ts.Close()
}

func (s *Store) Insert(timestamp time.Time, points []series.Point) error {
	if len(points) == 0 {
		return nil
	}
	rows := make([]tstorage.Row, 0, len(points))
	ts := timestamp.UnixMilli()
	s.mu.Lock()
	for _, point := range points {
		s.metrics[point.Key()] = point.Metric
		rows = append(rows, tstorage.Row{
			Metric:    point.Name,
			Labels:    point.TSLabels(),
			DataPoint: tstorage.DataPoint{Timestamp: ts, Value: point.Value},
		})
	}
	s.mu.Unlock()
	return s.ts.InsertRows(rows)
}

func (s *Store) Metrics() []series.Metric {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]series.Metric, 0, len(s.metrics))
	for _, metric := range s.metrics {
		out = append(out, cloneMetric(metric))
	}
	slices.SortFunc(out, func(a, b series.Metric) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		if a.Key() < b.Key() {
			return -1
		}
		if a.Key() > b.Key() {
			return 1
		}
		return 0
	})
	return out
}

func cloneMetric(metric series.Metric) series.Metric {
	if len(metric.Labels) == 0 {
		return metric
	}
	labels := make(map[string]string, len(metric.Labels))
	for key, value := range metric.Labels {
		labels[key] = value
	}
	metric.Labels = labels
	return metric
}

func (s *Store) Query(metric series.Metric, start, end time.Time) ([]tstorage.DataPoint, error) {
	points, err := s.ts.Select(metric.Name, metric.TSLabels(), start.UnixMilli(), end.UnixMilli())
	if errors.Is(err, tstorage.ErrNoDataPoints) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]tstorage.DataPoint, 0, len(points))
	for _, point := range points {
		if point != nil {
			out = append(out, *point)
		}
	}
	return out, nil
}
