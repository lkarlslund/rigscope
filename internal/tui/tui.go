package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	"github.com/lkarlslund/rigscope/internal/collectors"
	"github.com/lkarlslund/rigscope/internal/series"
)

// Options configures the live terminal dashboard.
type Options struct {
	ServerURL string
	Kitty     bool
}

type sampleData struct {
	Points          []series.Point      `json:"points"`
	CollectorErrors []map[string]string `json:"collector_errors"`
}

type wireEvent struct {
	Type string          `json:"type"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

type streamEvent struct {
	time   time.Time
	data   sampleData
	err    error
	status string
}

// Run connects to a rigscope daemon and owns the terminal until the user exits.
func Run(ctx context.Context, opts Options) error {
	events := make(chan streamEvent, 8)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go stream(streamCtx, opts.ServerURL, events)

	m := newModel(opts.ServerURL, opts.Kitty, events)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func stream(ctx context.Context, base string, out chan<- streamEvent) {
	delay := time.Second
	for ctx.Err() == nil {
		endpoint, err := websocketURL(base)
		if err != nil {
			send(ctx, out, streamEvent{err: err})
			return
		}
		headers := http.Header{"User-Agent": []string{"rigscope-tui"}}
		dialer := *websocket.DefaultDialer
		dialer.HandshakeTimeout = 750 * time.Millisecond
		conn, _, err := dialer.DialContext(ctx, endpoint, headers)
		if err != nil {
			send(ctx, out, streamEvent{status: "local"})
			localStream(ctx, out)
			return
		}
		delay = time.Second
		send(ctx, out, streamEvent{status: "connected"})
		for ctx.Err() == nil {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				_ = conn.Close()
				send(ctx, out, streamEvent{err: err, status: "reconnecting"})
				break
			}
			var event wireEvent
			if json.Unmarshal(payload, &event) != nil || event.Type != "sample" {
				continue
			}
			var data sampleData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				continue
			}
			send(ctx, out, streamEvent{time: time.UnixMilli(event.Time), data: data, status: "connected"})
		}
		if !wait(ctx, delay) {
			return
		}
	}
}

func localStream(ctx context.Context, out chan<- streamEvent) {
	found, err := collectors.Default(collectors.DefaultOptions{NVIDIA: true, ROCM: true, Zenpower: true})
	if err != nil {
		send(ctx, out, streamEvent{err: fmt.Errorf("detect collectors: %w", err), status: "error"})
		return
	}
	if len(found) == 0 {
		send(ctx, out, streamEvent{err: fmt.Errorf("no local collectors detected"), status: "error"})
		return
	}
	sampler := collectors.Sampler{Timeout: 800 * time.Millisecond}
	var rates series.CounterRateTransformer
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		timestamp := time.Now()
		raw := sampler.SampleAll(ctx, found)
		points := rates.Transform(timestamp, series.FlattenSample(raw))
		errors := localCollectorErrors(raw)
		send(ctx, out, streamEvent{time: timestamp, data: sampleData{Points: points, CollectorErrors: errors}, status: "local"})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func localCollectorErrors(sample map[string]any) []map[string]string {
	records, _ := sample["collectors"].([]map[string]any)
	var out []map[string]string
	for _, record := range records {
		errText, _ := record["error"].(string)
		if errText == "" {
			continue
		}
		name, _ := record["collector"].(string)
		out = append(out, map[string]string{"collector": name, "error": errText})
	}
	return out
}

func websocketURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", raw)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported server URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func send(ctx context.Context, out chan<- streamEvent, event streamEvent) {
	select {
	case out <- event:
	case <-ctx.Done():
	}
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
