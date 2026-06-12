package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lkarlslund/rigscope/internal/buildinfo"
	"github.com/lkarlslund/rigscope/internal/series"
)

func TestClientBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/build" {
			t.Fatalf("path = %q, want /api/build", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(buildinfo.Info{
			Version: "test",
			PID:     123,
		})
	}))
	defer srv.Close()

	got, err := (Client{BaseURL: srv.URL}).Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got.Version != "test" || got.PID != 123 {
		t.Fatalf("Build() = %+v, want version test pid 123", got)
	}
}

func TestClientMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics" {
			t.Fatalf("path = %q, want /api/metrics", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(MetricsResponse{
			Metrics: []series.Metric{{Name: "gpu_power_w", Unit: "W"}},
		})
	}))
	defer srv.Close()

	got, err := (Client{BaseURL: srv.URL}).Metrics(context.Background())
	if err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "gpu_power_w" {
		t.Fatalf("Metrics() = %+v, want gpu_power_w", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "", want: "http://127.0.0.1:7077"},
		{raw: "127.0.0.1:7077", want: "http://127.0.0.1:7077"},
		{raw: "http://localhost:7077/", want: "http://localhost:7077"},
	}

	for _, tt := range tests {
		if got := normalizeBaseURL(tt.raw); got != tt.want {
			t.Fatalf("normalizeBaseURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
