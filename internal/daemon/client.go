package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lkarlslund/rigscope/internal/buildinfo"
	"github.com/lkarlslund/rigscope/internal/series"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type MetricsResponse struct {
	Metrics []series.Metric `json:"metrics"`
}

func (c Client) Build(ctx context.Context) (buildinfo.Info, error) {
	var info buildinfo.Info
	if err := c.getJSON(ctx, "/api/build", &info); err != nil {
		return buildinfo.Info{}, err
	}
	return info, nil
}

func (c Client) Metrics(ctx context.Context) ([]series.Metric, error) {
	var response MetricsResponse
	if err := c.getJSON(ctx, "/api/metrics", &response); err != nil {
		return nil, err
	}
	return response.Metrics, nil
}

func (c Client) getJSON(ctx context.Context, path string, out any) error {
	endpoint, err := c.endpoint(path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GET %s: %s", endpoint, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

func (c Client) endpoint(path string) (string, error) {
	base := normalizeBaseURL(c.BaseURL)
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", c.BaseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "http://127.0.0.1:7077"
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return strings.TrimRight(raw, "/")
	}
	return "http://" + strings.TrimRight(raw, "/")
}

func NormalizeBaseURLForDisplay(raw string) string {
	return normalizeBaseURL(raw)
}
