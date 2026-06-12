package series

import (
	"strings"
	"time"
)

type CounterRateTransformer struct {
	prev map[string]counterSample
}

type counterSample struct {
	timestamp time.Time
	value     float64
}

func (t *CounterRateTransformer) Transform(timestamp time.Time, points []Point) []Point {
	if t.prev == nil {
		t.prev = map[string]counterSample{}
	}
	out := make([]Point, 0, len(points))
	for _, point := range points {
		if !isCounter(point.Metric) {
			out = append(out, point)
			continue
		}
		key := point.Key()
		prev, ok := t.prev[key]
		t.prev[key] = counterSample{timestamp: timestamp, value: point.Value}
		if !ok {
			continue
		}
		dt := timestamp.Sub(prev.timestamp).Seconds()
		if dt <= 0 || point.Value < prev.value {
			continue
		}
		out = append(out, Point{
			Metric: rateMetric(point.Metric),
			Value:  (point.Value - prev.value) / dt,
		})
	}
	return out
}

func isCounter(metric Metric) bool {
	return metric.Kind == "counter" || strings.HasSuffix(metric.Name, "_total")
}

func rateMetric(metric Metric) Metric {
	metric.Name = strings.TrimSuffix(metric.Name, "_total") + "_per_second"
	metric.Kind = "rate"
	switch metric.Unit {
	case "byte":
		metric.Unit = "byte/second"
		metric.Symbol = "B/s"
	case "second":
		metric.Unit = "second/second"
		if metric.Symbol == "" {
			metric.Symbol = "s/s"
		} else {
			metric.Symbol += "/s"
		}
	case "count":
		metric.Unit = "count/second"
		metric.Symbol = "count/s"
	default:
		if metric.Symbol != "" && !strings.HasSuffix(metric.Symbol, "/s") {
			metric.Symbol += "/s"
		}
		if metric.Unit != "" && !strings.Contains(metric.Unit, "/") {
			metric.Unit += "/second"
		}
	}
	return metric
}
