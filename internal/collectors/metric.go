package collectors

type Metric struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
	Unit   string            `json:"unit,omitempty"`
	Symbol string            `json:"symbol,omitempty"`
	Kind   string            `json:"kind,omitempty"`
}

func metric(name string, value float64, unit string, symbol string, kind string, labels map[string]string) map[string]any {
	if symbol == "" {
		symbol = defaultSymbol(unit)
	}
	out := map[string]any{
		"name":   name,
		"value":  value,
		"unit":   unit,
		"symbol": symbol,
		"kind":   kind,
	}
	if len(labels) > 0 {
		out["labels"] = labels
	}
	return out
}

func metricRecord(metrics []map[string]any) map[string]any {
	return map[string]any{"metrics": metrics}
}

func defaultSymbol(unit string) string {
	switch unit {
	case "count":
		return "count"
	case "ratio":
		return "1"
	case "second":
		return "s"
	default:
		return ""
	}
}
