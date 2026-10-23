package tui

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/rigscope/internal/series"
)

const historySize = 90

var (
	bg     = lipgloss.Color("#080B12")
	muted  = lipgloss.Color("#6D7890")
	text   = lipgloss.Color("#DCE7F7")
	cyan   = lipgloss.Color("#00E5FF")
	purple = lipgloss.Color("#B388FF")
	green  = lipgloss.Color("#48F0A4")
	orange = lipgloss.Color("#FFB454")
	red    = lipgloss.Color("#FF5D7D")
)

type model struct {
	server            string
	kitty             bool
	events            <-chan streamEvent
	width, height     int
	status, lastError string
	lastSample        time.Time
	values            map[string]series.Point
	history           map[string][]float64
	errors            []map[string]string
	paused, help      bool
}

func newModel(server string, kitty bool, events <-chan streamEvent) model {
	return model{server: server, kitty: kitty, events: events, status: "connecting", values: map[string]series.Point{}, history: map[string][]float64{}}
}

func (m model) Init() tea.Cmd { return waitEvent(m.events) }

func waitEvent(events <-chan streamEvent) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.help = !m.help
		case " ":
			m.paused = !m.paused
		}
	case streamEvent:
		m.status = msg.status
		if msg.err != nil {
			m.lastError = msg.err.Error()
		}
		if !msg.time.IsZero() && !m.paused {
			m.lastSample, m.errors = msg.time, msg.data.CollectorErrors
			for _, point := range msg.data.Points {
				key := point.Key()
				m.values[key] = point
				h := append(m.history[key], point.Value)
				if len(h) > historySize {
					h = h[len(h)-historySize:]
				}
				m.history[key] = h
			}
		}
		return m, waitEvent(m.events)
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		return "Starting rigscope…"
	}
	if m.width < 62 || m.height < 16 {
		return m.compactView()
	}
	header := m.header()
	footer := m.footer()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	var body string
	if m.help {
		body = m.helpView(bodyHeight)
	} else {
		body = m.overview(bodyHeight)
	}
	out := lipgloss.NewStyle().Background(bg).Foreground(text).Render(header + "\n" + body + "\n" + footer)
	return out
}

func (m model) header() string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(cyan).Render(" RIG") + lipgloss.NewStyle().Bold(true).Foreground(purple).Render("SCOPE")
	mode := "LIVE"
	color := green
	if m.paused {
		mode, color = "PAUSED", orange
	} else if m.status == "local" {
		mode, color = "LOCAL", cyan
	} else if m.status != "connected" {
		mode, color = strings.ToUpper(m.status), red
	}
	badge := lipgloss.NewStyle().Bold(true).Foreground(bg).Background(color).Padding(0, 1).Render(mode)
	clock := "waiting for telemetry"
	if !m.lastSample.IsZero() {
		clock = "sample " + m.lastSample.Format("15:04:05")
	}
	left := brand + "  " + badge + "  " + lipgloss.NewStyle().Foreground(muted).Render(clock)
	right := lipgloss.NewStyle().Foreground(muted).Render(shortServer(m.server))
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	return lipgloss.NewStyle().Width(m.width).Background(bg).Render(left + strings.Repeat(" ", gap) + right)
}

func (m model) overview(height int) string {
	gap := 1
	groups := m.gpuGroups()
	cardCount := 5 + len(groups)
	if m.hasPrefix("npu_") {
		cardCount++
	}
	maxCols := max(1, m.width/24)
	cols := min(cardCount, maxCols)
	// Prefer card proportions near 2.8:1 while guaranteeing every card fits.
	bestScore := math.Inf(1)
	for candidate := 1; candidate <= maxCols && candidate <= cardCount; candidate++ {
		rows := (cardCount + candidate - 1) / candidate
		w := float64((m.width - (candidate-1)*gap) / candidate)
		h := float64((height - (rows-1)*gap) / rows)
		if h < 4 {
			continue
		}
		score := math.Abs(w/max(1, h) - 2.8)
		emptySlots := rows*candidate - cardCount
		score += 3 * float64(emptySlots) / float64(cardCount)
		if score < bestScore {
			bestScore, cols = score, candidate
		}
	}
	rowCount := (cardCount + cols - 1) / cols
	cardHeight := max(4, (height-(rowCount-1)*gap)/rowCount)
	type cardBuilder func(width, height int) string
	builders := []cardBuilder{
		func(w, h int) string { return m.computeCard("CPU", "cpu", w, h, graphPlacement{}) },
		func(w, h int) string {
			return m.memoryCard("SYSTEM MEMORY", "memory_used_bytes", "memory_total_bytes", w, h, cyan, graphPlacement{})
		},
	}
	for _, group := range groups {
		group := group
		builders = append(builders, func(w, h int) string { return m.gpuCard(group, w, h, graphPlacement{}) })
	}
	builders = append(builders,
		func(w, h int) string { return m.ioCard(w, h, graphPlacement{}) },
		func(w, h int) string { return m.powerCard(w, h, graphPlacement{}) },
		func(w, h int) string { return m.thermalCard(w, h, graphPlacement{}) },
	)
	if m.hasPrefix("npu_") {
		builders = append(builders, func(w, h int) string { return m.npuCard(w, h) })
	}
	rows := make([]string, 0, rowCount)
	for i := 0; i < len(builders); i += cols {
		end := min(i+cols, len(builders))
		count := end - i
		cardWidth := (m.width - (count-1)*gap) / count
		row := make([]string, 0, count*2-1)
		for j := i; j < end; j++ {
			if len(row) > 0 {
				row = append(row, strings.Repeat(" ", gap))
			}
			row = append(row, builders[j](cardWidth, cardHeight))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	content := strings.Join(rows, "\n")
	content = cropHeight(content, height)
	return content
}

func (m model) computeCard(title, _ string, width, height int, placement graphPlacement) string {
	busy := 0.0
	if m.hasName("cpu_idle_pct") {
		busy = 100 - m.first("cpu_idle_pct")
	}
	if busy < 0 || busy > 100 {
		busy = 0
	}
	cores := m.first("cpu_cores")
	values := m.historyFor("cpu_idle_pct")
	for i := range values {
		values[i] = 100 - values[i]
	}
	plot := m.cardGraph(singleGraphSeries("CPU", values, graphPalette[0], "%"), width-4, max(2, height-8), 9201, placement)
	items := []string{
		metricLine("LOAD", busy, "%", cyan, width-4),
		m.progressBar(busy, 100, width-4, cyan),
		plot,
		pairs(width-4, "USER", percent(m.first("cpu_user_pct")), "SYSTEM", percent(m.first("cpu_system_pct"))),
		pairs(width-4, "IO WAIT", percent(m.first("cpu_iowait_pct")), "CORES", fmt.Sprintf("%.0f", cores)),
	}
	return card(title, items, width, height, cyan)
}

func (m model) memoryCard(title, usedName, totalName string, width, height int, color lipgloss.Color, placement graphPlacement) string {
	used, total := m.first(usedName), m.first(totalName)
	pct := ratio(used, total)
	plot := m.cardGraph(singleGraphSeries("memory", m.historyFor(usedName), graphPalette[0], "B"), width-4, max(2, height-7), 9202, placement)
	items := []string{metricLine("USED", pct, "%", color, width-4), m.progressBar(pct, 100, width-4, color), plot, pairs(width-4, "USED", bytes(used), "TOTAL", bytes(total))}
	return card(title, items, width, height, color)
}

type gpuGroup struct {
	label, identity string
	points          []series.Point
}

func (m model) gpuGroups() []gpuGroup {
	groups := map[string]*gpuGroup{}
	for _, p := range m.values {
		if !strings.HasPrefix(p.Name, "gpu_") {
			continue
		}
		id := p.Labels["collector"] + "/" + firstNonempty(p.Labels["index"], p.Labels["card"], "0")
		g := groups[id]
		if g == nil {
			g = &gpuGroup{identity: id, label: firstNonempty(p.Labels["device"], p.Labels["name"], strings.ToUpper(id))}
			groups[id] = g
		}
		g.points = append(g.points, p)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]gpuGroup, 0, len(keys))
	for _, k := range keys {
		out = append(out, *groups[k])
	}
	return out
}

func (m model) gpuCard(g gpuGroup, width, height int, placement graphPlacement) string {
	util := pointValue(g.points, "gpu_util_pct")
	used := firstNonzero(pointValue(g.points, "gpu_vram_used_bytes"), pointValue(g.points, "gpu_mem_used_mib")*1024*1024)
	total := pointValue(g.points, "gpu_vram_total_bytes")
	memText := bytes(used)
	if total > 0 {
		memText += " / " + bytes(total)
	}
	var utilHistory []float64
	for _, point := range g.points {
		if point.Name == "gpu_util_pct" {
			utilHistory = m.history[point.Key()]
			break
		}
	}
	plot := m.cardGraph(singleGraphSeries(g.label, utilHistory, graphPalette[1], "%"), width-4, max(2, height-8), 9300+stableGraphID(g.identity)%500, placement)
	items := []string{metricLine("LOAD", util, "%", purple, width-4), m.progressBar(util, 100, width-4, purple), plot, pairs(width-4, "VRAM", memText, "TEMP", degrees(firstNonzero(pointValue(g.points, "gpu_temp_c"), pointValue(g.points, "gpu_temp_celsius")))), pairs(width-4, "POWER", watts(pointValue(g.points, "gpu_power_w")), "CLOCK", mhz(pointValue(g.points, "gpu_sm_clock_mhz")))}
	return card(truncate("GPU · "+g.label, width-5), items, width, height, purple)
}

func (m model) ioCard(width, height int, placement graphPlacement) string {
	read := m.sum("disk_read_bytes_per_second")
	write := m.sum("disk_written_bytes_per_second")
	rx := m.sum("network_rx_bytes_per_second")
	tx := m.sum("network_tx_bytes_per_second")
	data, _, _ := m.graphData("io")
	plot := m.cardGraph(data, width-4, max(2, height-7), 9203, placement)
	items := []string{metricLine("TOTAL", read+write+rx+tx, "B/s", green, width-4), plot, pairs(width-4, "READ", bytesRate(read), "WRITE", bytesRate(write)), pairs(width-4, "NET ↓", bytesRate(rx), "NET ↑", bytesRate(tx))}
	return card("I/O THROUGHPUT", items, width, height, green)
}

func (m model) powerCard(width, height int, placement graphPlacement) string {
	// Wall power from a PSU already includes every component.
	power := m.sumKind("power_input")
	if power <= 0 {
		power = m.sumKind("power")
	}
	power = max(0, power)
	powerData, _, _ := m.graphData("power")
	plot := m.cardGraph(powerData, width-4, max(2, height-5), 9204, placement)
	items := []string{metricLine("TOTAL", power, "W", orange, width-4), plot}
	return card("POWER", items, width, height, orange)
}

func (m model) thermalCard(width, height int, placement graphPlacement) string {
	temps := m.namedValues("temperature")
	tempData, _, _ := m.graphData("thermal")
	tempLabel := "WAITING FOR SENSORS"
	if len(temps) > 0 {
		tempLabel = truncate(sensorName(temps[0]), max(4, width-13)) + "  " + degrees(temps[0].Value)
	}
	items := []string{lipgloss.NewStyle().Foreground(muted).Render(tempLabel), m.cardGraph(tempData, width-4, max(2, height-5), 9205, placement)}
	return card("THERMALS", items, width, height, red)
}

func (m model) npuCard(width, height int) string {
	active := m.first("npu_runtime_active")
	state := "SUSPENDED"
	color := muted
	if active > 0 {
		state, color = "ACTIVE", green
	}
	items := []string{metricLine("STATE", active, state, color, width-4), "", lipgloss.NewStyle().Foreground(muted).Render("Kernel exposes runtime state; direct busy"), lipgloss.NewStyle().Foreground(muted).Render("telemetry is not available on this driver.")}
	return card("NPU", items, width, height, color)
}

func (m model) helpView(height int) string {
	renderer := "Unicode Braille"
	if m.kitty {
		renderer += " · Kitty truecolor"
	}
	lines := []string{"", lipgloss.NewStyle().Bold(true).Foreground(cyan).Render("  RIGSCOPE CONTROL DECK"), "", "  q / ctrl-c     quit", "  space          pause live updates", "  ?              toggle this help", "", "  Every live graph stays on this dashboard. It consumes the daemon", "  stream when available and otherwise collects locally.", "", "  Renderer: " + renderer}
	return cropHeight(strings.Join(lines, "\n"), height)
}

func (m model) footer() string {
	left := " [space] pause   [?] help   [q] quit"
	right := fmt.Sprintf("%d series", len(m.values))
	if len(m.errors) > 0 {
		right = fmt.Sprintf("%d collector errors", len(m.errors))
	}
	if len(left)+len(right)+1 > m.width {
		right = ""
	}
	gap := max(0, m.width-len(left)-len(right))
	return lipgloss.NewStyle().Foreground(muted).Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

func (m model) compactView() string {
	cpu := 100 - m.first("cpu_idle_pct")
	mem := ratio(m.first("memory_used_bytes"), m.first("memory_total_bytes"))
	return fmt.Sprintf("RIGSCOPE  %s\nCPU %5.1f%% %s\nMEM %5.1f%% %s\n\nResize to at least 62×16 · q quit\n", strings.ToUpper(m.status), cpu, tinyBar(cpu, 20), mem, tinyBar(mem, 20))
}

func (m model) first(name string) float64 {
	for _, p := range m.values {
		if p.Name == name {
			return p.Value
		}
	}
	return 0
}
func (m model) sum(name string) float64 {
	var n float64
	for _, p := range m.values {
		if p.Name == name {
			n += p.Value
		}
	}
	return n
}
func (m model) sumKind(kind string) float64 {
	var n float64
	for _, p := range m.values {
		if p.Kind == kind {
			n += p.Value
		}
	}
	return n
}
func (m model) hasPrefix(prefix string) bool {
	for _, p := range m.values {
		if strings.HasPrefix(p.Name, prefix) {
			return true
		}
	}
	return false
}
func (m model) hasName(name string) bool {
	for _, p := range m.values {
		if p.Name == name {
			return true
		}
	}
	return false
}
func (m model) namedValues(kind string) []series.Point {
	var out []series.Point
	for _, p := range m.values {
		if p.Kind == kind && (kind != "temperature" || (p.Value >= 1 && p.Value < 200)) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	return out
}
func (m model) sparkFor(name string, width int, invert bool, color lipgloss.Color) string {
	for key, p := range m.values {
		if p.Name == name {
			vals := append([]float64{}, m.history[key]...)
			if invert {
				for i := range vals {
					vals[i] = 100 - vals[i]
				}
			}
			return spark(vals, width, color)
		}
	}
	return spark(nil, width, color)
}
func (m model) sparkMetric(points []series.Point, name string, width int, color lipgloss.Color) string {
	for _, p := range points {
		if p.Name == name {
			return spark(m.history[p.Key()], width, color)
		}
	}
	return spark(nil, width, color)
}
func (m model) sparkCombined(names []string, width int, color lipgloss.Color) string {
	var best []float64
	for k, p := range m.values {
		for _, n := range names {
			if p.Name == n && len(m.history[k]) > len(best) {
				best = m.history[k]
			}
		}
	}
	return spark(best, width, color)
}

func (m model) historyFor(name string) []float64 {
	for key, point := range m.values {
		if point.Name == name {
			return append([]float64(nil), m.history[key]...)
		}
	}
	return nil
}

func singleGraphSeries(name string, values []float64, graphColor color.RGBA, unit string) []graphSeries {
	if len(values) == 0 {
		return nil
	}
	return []graphSeries{{name: name, values: values, color: graphColor, unit: unit}}
}

func card(title string, lines []string, width, height int, color lipgloss.Color) string {
	inner := width - 2
	innerHeight := max(2, height-2)
	lineWidth := max(1, width-4)
	expanded := make([]string, 0, len(lines))
	for _, line := range lines {
		for _, physical := range strings.Split(line, "\n") {
			expanded = append(expanded, ansi.Truncate(physical, lineWidth, ""))
		}
	}
	body := cropHeight(lipgloss.JoinVertical(lipgloss.Left, expanded...), max(1, innerHeight-1))
	style := lipgloss.NewStyle().Width(inner).Height(innerHeight).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#273249")).Background(bg)
	titleText := lipgloss.NewStyle().Bold(true).Foreground(color).Render(title)
	return style.Render(titleText + "\n" + body)
}
func metricLine(label string, value float64, symbol string, color lipgloss.Color, width int) string {
	l := lipgloss.NewStyle().Foreground(muted).Render(label)
	v := lipgloss.NewStyle().Bold(true).Foreground(color).Render(formatValue(value, symbol))
	return l + strings.Repeat(" ", max(1, width-lipgloss.Width(l)-lipgloss.Width(v))) + v
}
func (m model) progressBar(value, maxValue float64, width int, color lipgloss.Color) string {
	width = max(4, width)
	n := int(math.Round(clamp(value/maxValue, 0, 1) * float64(width)))
	if m.kitty && n > 0 {
		// Kitty's reliable 24-bit color support gets a per-cell neon gradient.
		var b strings.Builder
		for i := 0; i < n; i++ {
			t := float64(i) / float64(max(1, width-1))
			r, g, bl := int(0+179*t), int(229-93*t), int(255)
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, bl))).Render("━"))
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#20283A")).Render(strings.Repeat("━", width-n)))
		return b.String()
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("━", n)) + lipgloss.NewStyle().Foreground(lipgloss.Color("#20283A")).Render(strings.Repeat("━", width-n))
}
func tinyBar(v float64, w int) string {
	n := int(clamp(v/100, 0, 1) * float64(w))
	return strings.Repeat("█", n) + strings.Repeat("░", w-n)
}
func spark(vals []float64, width int, color lipgloss.Color) string {
	chars := []rune("▁▂▃▄▅▆▇█")
	if len(vals) == 0 {
		return lipgloss.NewStyle().Foreground(muted).Render(strings.Repeat("·", max(1, width)))
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", max(0, width-len(vals))))
	for _, v := range vals {
		i := 0
		if hi > lo {
			i = int((v - lo) / (hi - lo) * 7)
		}
		b.WriteRune(chars[i])
	}
	return lipgloss.NewStyle().Foreground(color).Render(b.String())
}
func pairs(width int, l1, v1, l2, v2 string) string {
	half := width / 2
	a := lipgloss.NewStyle().Foreground(muted).Render(l1+" ") + v1
	b := ""
	if l2 != "" {
		b = lipgloss.NewStyle().Foreground(muted).Render(l2+" ") + v2
	}
	return truncate(a, half) + strings.Repeat(" ", max(1, half-lipgloss.Width(truncate(a, half)))) + truncate(b, width-half)
}
func pointValue(points []series.Point, name string) float64 {
	for _, p := range points {
		if p.Name == name {
			return p.Value
		}
	}
	return 0
}
func ratio(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return 100 * a / b
}
func bytes(v float64) string     { return human(v, "B") }
func bytesRate(v float64) string { return human(v, "B/s") }
func human(v float64, suffix string) string {
	units := []string{"", "Ki", "Mi", "Gi", "Ti"}
	i := 0
	for math.Abs(v) >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s%s", v, units[i], suffix)
}
func percent(v float64) string { return fmt.Sprintf("%.1f%%", v) }
func degrees(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f°C", v)
}
func watts(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f W", v)
}
func mhz(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f MHz", v)
}
func formatValue(v float64, s string) string {
	if s == "B" {
		return bytes(v)
	}
	if s == "B/s" {
		return bytesRate(v)
	}
	if s == "%" {
		return percent(v)
	}
	if math.Abs(v) >= 1000 {
		return fmt.Sprintf("%.0f %s", v, s)
	}
	return fmt.Sprintf("%.1f %s", v, s)
}
func metricLabel(p series.Point) string {
	label := p.Name
	for _, k := range []string{"device", "name", "card", "interface", "device_path", "mountpoint", "sensor"} {
		if p.Labels[k] != "" {
			label += " · " + p.Labels[k]
			break
		}
	}
	return label
}
func sensorName(p series.Point) string {
	return firstNonempty(p.Labels["sensor"], p.Labels["type"], p.Labels["chip"], p.Name)
}
func firstNonempty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return "—"
}
func firstNonzero(v ...float64) float64 {
	for _, n := range v {
		if n != 0 {
			return n
		}
	}
	return 0
}
func shortServer(s string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s, "http://"), "https://"), "/")
}
func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return ansi.Truncate(s, w, "…")
}
func cropHeight(s string, h int) string {
	return cropWindow(s, h, 0)
}

func cropWindow(s string, h, offset int) string {
	lines := strings.Split(s, "\n")
	offset = min(max(0, offset), max(0, len(lines)-h))
	lines = lines[offset:]
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
func intersperse(items []string, sep string) []string {
	if len(items) < 2 {
		return items
	}
	out := make([]string, 0, len(items)*2-1)
	for i, x := range items {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, x)
	}
	return out
}
func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
