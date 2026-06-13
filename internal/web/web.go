package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nakabonne/tstorage"

	"github.com/lkarlslund/rigscope/internal/buildinfo"
	"github.com/lkarlslund/rigscope/internal/series"
	"github.com/lkarlslund/rigscope/internal/store"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	Store      *store.Store
	LayoutPath string
	Hub        *Hub

	assetsHash string
}

const maxHistoricalPointSpacing = 5 * time.Minute

func (s *Server) Handler() http.Handler {
	if s.Hub == nil {
		s.Hub = NewHub()
	}
	s.assetsHash = AssetsHash()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/ws", s.ws)
	mux.HandleFunc("/api/build", s.build)
	mux.HandleFunc("/api/metrics", s.metrics)
	mux.HandleFunc("/api/catalog", s.catalog)
	mux.HandleFunc("/api/query", s.query)
	mux.HandleFunc("/api/query/batch", s.queryBatch)
	mux.HandleFunc("/api/graphs/defaults", s.defaultGraphs)
	mux.HandleFunc("/api/graphs/layout", s.layout)
	staticFiles := http.FileServer(http.FS(mustSubFS(staticFS, "static")))
	mux.Handle("/static/", http.StripPrefix("/static/", cacheStatic(staticFiles)))
	return mux
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	return s.ListenAndServeContext(addr)(ctx)
}

func (s *Server) ListenAndServeContext(addr string) func(context.Context) error {
	return func(ctx context.Context) error {
		return s.listenAndServe(ctx, addr)
	}
}

func (s *Server) listenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) PublishSample(timestamp time.Time, points []series.Point, collectorErrors []map[string]string) {
	if s.Hub == nil {
		return
	}
	s.Hub.Broadcast(WSEvent{
		Type: "sample",
		Time: timestamp.UnixMilli(),
		Data: map[string]any{
			"points":           points,
			"collector_errors": collectorErrors,
		},
	})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"metrics": s.metricsList()})
}

func (s *Server) catalog(w http.ResponseWriter, _ *http.Request) {
	metrics := s.metricsList()
	groups := map[string][]series.Metric{}
	for _, metric := range metrics {
		group := metric.Kind
		if group == "" {
			group = metric.Labels["collector"]
		}
		if group == "" {
			group = "other"
		}
		groups[group] = append(groups[group], metric)
	}
	writeJSON(w, map[string]any{
		"metrics":  metrics,
		"groups":   groups,
		"defaults": DefaultGraphs(metrics),
	})
}

func (s *Server) build(w http.ResponseWriter, _ *http.Request) {
	info := buildinfo.Current()
	writeJSON(w, map[string]any{
		"version":     info.Version,
		"commit":      info.Commit,
		"built_at":    info.BuiltAt,
		"started_at":  info.StartedAt,
		"pid":         info.PID,
		"assets_hash": s.assetHash(),
		"api_version": 1,
	})
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := query.Get("metric")
	if name == "" {
		http.Error(w, "missing metric", http.StatusBadRequest)
		return
	}
	end := time.Now()
	if raw := query.Get("end"); raw != "" {
		parsed, err := parseUnixMillis(raw)
		if err != nil {
			http.Error(w, "invalid end", http.StatusBadRequest)
			return
		}
		end = parsed
	}
	start := end.Add(-10 * time.Minute)
	if raw := query.Get("start"); raw != "" {
		parsed, err := parseUnixMillis(raw)
		if err != nil {
			http.Error(w, "invalid start", http.StatusBadRequest)
			return
		}
		start = parsed
	}
	metric := series.Metric{Name: name, Labels: map[string]string{}}
	for key, values := range query {
		if len(values) == 0 || values[0] == "" {
			continue
		}
		if key == "metric" || key == "start" || key == "end" {
			continue
		}
		metric.Labels[key] = values[0]
	}
	points, err := s.queryMetric(metric, start, end, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"metric": metric,
		"start":  start.UnixMilli(),
		"end":    end.UnixMilli(),
		"points": points,
	})
}

func (s *Server) queryBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req BatchQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(req.Series) == 0 {
		http.Error(w, "missing series", http.StatusBadRequest)
		return
	}
	end := time.Now()
	if req.End > 0 {
		end = time.UnixMilli(req.End)
	}
	start := end.Add(-10 * time.Minute)
	if req.Start > 0 {
		start = time.UnixMilli(req.Start)
	}
	out := BatchQueryResponse{
		Start:  start.UnixMilli(),
		End:    end.UnixMilli(),
		Series: make([]BatchSeriesResponse, 0, len(req.Series)),
	}
	for _, item := range req.Series {
		if item.Metric.Name == "" {
			http.Error(w, "series metric name is required", http.StatusBadRequest)
			return
		}
		points, err := s.queryMetric(item.Metric, start, end, item.Transform)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out.Series = append(out.Series, BatchSeriesResponse{
			ID:     item.ID,
			Metric: item.Metric,
			Points: limitPoints(points, maxPointsForRange(start, end, req.MaxPoints)),
		})
	}
	writeJSON(w, out)
}

func (s *Server) defaultGraphs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"graphs": DefaultGraphs(s.metricsList())})
}

func (s *Server) layout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		layout, err := s.LoadLayout()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, layout)
	case http.MethodPut:
		var layout DashboardLayout
		limited := http.MaxBytesReader(w, r.Body, 4<<20)
		if err := json.NewDecoder(limited).Decode(&layout); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := s.SaveLayout(layout); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	html := strings.ReplaceAll(string(data), "{{ASSETS_HASH}}", s.assetHash())
	_, _ = io.WriteString(w, html)
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	if s.Hub == nil {
		s.Hub = NewHub()
	}
	s.Hub.ServeHTTP(w, r, WSEvent{
		Type: "hello",
		Time: time.Now().UnixMilli(),
		Data: map[string]any{
			"assets_hash": s.assetHash(),
			"build":       buildinfo.Current(),
			"api_version": 1,
		},
	})
}

func (s *Server) assetHash() string {
	if s.assetsHash == "" {
		s.assetsHash = AssetsHash()
	}
	return s.assetsHash
}

func (s *Server) metricsList() []series.Metric {
	if s.Store == nil {
		return nil
	}
	return s.Store.Metrics()
}

func (s *Server) queryMetric(metric series.Metric, start, end time.Time, transform string) ([][2]float64, error) {
	if s.Store == nil {
		return nil, nil
	}
	points, err := s.Store.Query(metric, start, end)
	if err != nil {
		return nil, err
	}
	if transform == "rate" || strings.HasSuffix(metric.Name, "_total") {
		return ratePoints(points), nil
	}
	encoded := make([][2]float64, 0, len(points))
	for _, point := range points {
		encoded = append(encoded, [2]float64{float64(point.Timestamp), point.Value})
	}
	return encoded, nil
}

func (s *Server) LoadLayout() (DashboardLayout, error) {
	if s.LayoutPath == "" {
		return DefaultLayout(), nil
	}
	data, err := os.ReadFile(s.LayoutPath)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultLayout(), nil
	}
	if err != nil {
		return DashboardLayout{}, err
	}
	var layout DashboardLayout
	if err := json.Unmarshal(data, &layout); err != nil {
		return DashboardLayout{}, err
	}
	return normalizeLayout(layout), nil
}

func (s *Server) SaveLayout(layout DashboardLayout) error {
	if s.LayoutPath == "" {
		return nil
	}
	layout = normalizeLayout(layout)
	data, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.LayoutPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.LayoutPath, append(data, '\n'), 0o644)
}

func AssetsHash() string {
	hash := sha256.New()
	files := []string{}
	_ = fs.WalkDir(staticFS, "static", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files = append(files, name)
		return nil
	})
	slices.Sort(files)
	for _, name := range files {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			continue
		}
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func parseUnixMillis(raw string) (time.Time, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(value), nil
}

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func ratePoints(points []tstorage.DataPoint) [][2]float64 {
	if len(points) < 2 {
		return nil
	}
	out := make([][2]float64, 0, len(points)-1)
	prev := points[0]
	for _, point := range points[1:] {
		dt := float64(point.Timestamp-prev.Timestamp) / 1000
		if dt <= 0 || point.Value < prev.Value {
			prev = point
			continue
		}
		out = append(out, [2]float64{float64(point.Timestamp), (point.Value - prev.Value) / dt})
		prev = point
	}
	return out
}

func maxPointsForRange(start, end time.Time, requested int) int {
	if requested <= 0 {
		return requested
	}
	if !end.After(start) {
		return requested
	}
	pointsForFiveMinuteSpacing := int(end.Sub(start)/maxHistoricalPointSpacing) + 1
	if end.Sub(start)%maxHistoricalPointSpacing != 0 {
		pointsForFiveMinuteSpacing++
	}
	return max(requested, pointsForFiveMinuteSpacing)
}

func limitPoints(points [][2]float64, maxPoints int) [][2]float64 {
	if maxPoints <= 0 || len(points) <= maxPoints {
		return points
	}
	step := float64(len(points)-1) / float64(maxPoints-1)
	out := make([][2]float64, 0, maxPoints)
	for i := range maxPoints {
		idx := int(float64(i) * step)
		if idx >= len(points) {
			idx = len(points) - 1
		}
		out = append(out, points[idx])
	}
	return out
}

func metricDisplayName(name string) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func metricDisplayNameForTransform(name string, transform string) string {
	if transform == "rate" {
		name = strings.TrimSuffix(name, "_total")
	}
	return metricDisplayName(name)
}

func metricLegend(metric series.Metric, transform string) string {
	summary := metricLabelSummary(metric)
	if summary == "" {
		if compact := compactMetricName(metric.Name, transform); compact != metricDisplayNameForTransform(metric.Name, transform) {
			return compact
		}
		return metricDisplayNameForTransform(metric.Name, transform) + labelSuffix(metric)
	}
	switch {
	case strings.HasPrefix(metric.Name, "gpu_"):
		return summary
	case metric.Name == "cpu_package_power_w":
		return summary
	case metric.Name == "hwmon_power_w":
		return summary
	case metric.Name == "temperature_celsius", strings.HasPrefix(metric.Name, "gpu_temp"):
		return summary
	case strings.HasPrefix(metric.Name, "disk_"):
		return compactMetricName(metric.Name, transform) + " " + summary
	case strings.HasPrefix(metric.Name, "network_"):
		return compactMetricName(metric.Name, transform) + " " + summary
	case metric.Name == "filesystem_used_bytes", metric.Name == "filesystem_free_bytes":
		return compactMetricName(metric.Name, transform) + " " + summary
	default:
		return metricDisplayNameForTransform(metric.Name, transform) + " " + summary
	}
}

func compactMetricName(name string, transform string) string {
	if transform == "rate" {
		name = strings.TrimSuffix(name, "_total")
	}
	replacer := strings.NewReplacer(
		"disk_read_bytes_per_second", "Read",
		"disk_written_bytes_per_second", "Write",
		"disk_reads_per_second", "Reads",
		"disk_writes_per_second", "Writes",
		"network_rx_bytes_per_second", "RX",
		"network_tx_bytes_per_second", "TX",
		"network_rx_packets_per_second", "RX packets",
		"network_tx_packets_per_second", "TX packets",
		"network_rx_errors_per_second", "RX errors",
		"network_tx_errors_per_second", "TX errors",
		"network_rx_drops_per_second", "RX drops",
		"network_tx_drops_per_second", "TX drops",
		"filesystem_used_bytes", "Used",
		"filesystem_free_bytes", "Free",
		"sockets_used", "Sockets",
		"tcp_sockets_in_use", "TCP sockets",
		"tcp_sockets_time_wait", "TCP socket TW",
		"udp_sockets_in_use", "UDP sockets",
		"tcp_connections_established", "TCP established",
		"tcp_connections_listen", "TCP listen",
		"tcp_connections_time_wait", "TCP time-wait",
	)
	if compact := replacer.Replace(name); compact != name {
		return compact
	}
	return metricDisplayName(name)
}

func labelSuffix(metric series.Metric) string {
	if len(metric.Labels) == 0 {
		return ""
	}
	if summary := metricLabelSummary(metric); summary != "" {
		return " " + summary
	}
	keys := make([]string, 0, len(metric.Labels))
	for key := range metric.Labels {
		if key != "collector" && key != "index" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	parts := []string{}
	for _, key := range keys {
		value := shortLabelValue(metric.Labels[key])
		if value != "" && !slices.Contains(parts, value) {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func metricLabelSummary(metric series.Metric) string {
	labels := metric.Labels
	switch labels["collector"] {
	case "zenpower":
		if metric.Name == "cpu_package_power_w" {
			return "CPU package"
		}
	case "nvidia":
		return compactDeviceName(labels["device"])
	case "drm", "rocm":
		if labels["driver"] == "amdgpu" || labels["chip"] == "amdgpu" || labels["collector"] == "rocm" {
			if labels["sensor"] != "" {
				return "AMD GPU " + labels["sensor"]
			}
			return "AMD GPU"
		}
	case "thermal":
		if labels["chip"] == "nvme" {
			if labels["sensor"] != "" {
				return "NVMe " + labels["sensor"]
			}
			return "NVMe"
		}
		if labels["type"] != "" {
			return shortLabelValue(labels["type"])
		}
		if labels["chip"] != "" && labels["sensor"] != "" {
			return shortLabelValue(labels["chip"]) + " " + shortLabelValue(labels["sensor"])
		}
	case "network":
		if labels["interface"] != "" {
			return labels["interface"]
		}
	case "disk":
		if labels["device"] != "" {
			return labels["device"]
		}
	case "filesystem":
		if labels["mount"] != "" {
			return labels["mount"]
		}
	case "xdna":
		if labels["vbnv"] != "" {
			return labels["vbnv"]
		}
		if labels["driver"] != "" {
			return labels["driver"]
		}
	}
	if metric.Name == "hwmon_power_w" && labels["chip"] != "" {
		if labels["sensor"] != "" {
			return shortLabelValue(labels["chip"]) + " " + shortLabelValue(labels["sensor"])
		}
		return shortLabelValue(labels["chip"])
	}
	return ""
}

func compactDeviceName(value string) string {
	value = shortLabelValue(value)
	replacements := []string{
		"NVIDIA ", "",
		"GeForce ", "",
		"RTX PRO ", "RTX ",
		"Blackwell Workstation Edition", "",
		"Workstation Edition", "",
		"Graphics / Radeon ", "/",
		" Graphics", "",
	}
	replacer := strings.NewReplacer(replacements...)
	value = strings.Join(strings.Fields(replacer.Replace(value)), " ")
	return strings.TrimSpace(value)
}

func shortLabelValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"NVIDIA ", "",
		"Advanced Micro Devices, Inc. ", "AMD ",
		"Graphics / Radeon ", "/",
		" Graphics", "",
	)
	return strings.Join(strings.Fields(replacer.Replace(value)), " ")
}

func graphID(prefix string, metric series.Metric) string {
	id := strings.ToLower(prefix + "-" + metric.Key())
	replacer := strings.NewReplacer("{", "-", "}", "", "=", "-", " ", "-", "/", "-", ".", "-")
	return path.Clean("/" + replacer.Replace(id))[1:]
}

func graphColor(i int) string {
	colors := []string{"#38bdf8", "#22c55e", "#f59e0b", "#ef4444", "#a78bfa", "#14b8a6", "#f97316", "#e879f9"}
	return colors[i%len(colors)]
}
