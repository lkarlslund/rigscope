package tui

import (
	"fmt"
	"hash/crc32"
	"image/color"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/rigscope/internal/series"
)

type graphSeries struct {
	name   string
	values []float64
	color  color.RGBA
	unit   string
}

type graphPlacement struct{ row, col int }

var graphPalette = []color.RGBA{
	{0x00, 0xe5, 0xff, 0xff},
	{0xb3, 0x88, 0xff, 0xff},
	{0x48, 0xf0, 0xa4, 0xff},
	{0xff, 0xb4, 0x54, 0xff},
	{0xff, 0x5d, 0x7d, 0xff},
	{0x65, 0x8d, 0xff, 0xff},
}

func (m model) graphView(kind string, height int) string {
	series, title, unit := m.graphData(kind)
	if len(series) == 0 {
		return cropHeight(lipgloss.NewStyle().Foreground(muted).Padding(2).Render("Waiting for "+strings.ToLower(title)+" telemetry…"), height)
	}
	chartHeight := max(6, height-3)
	legend := graphLegend(series, max(20, m.width-2))
	heading := lipgloss.NewStyle().Bold(true).Foreground(graphColor(kind)).Render(" "+title) + "  " + lipgloss.NewStyle().Foreground(muted).Render("last 90 samples · "+unit)
	plot := brailleGraph(series, max(20, m.width), chartHeight)
	return cropHeight(heading+"\n"+legend+"\n"+plot, height)
}

func (m model) graphData(kind string) ([]graphSeries, string, string) {
	switch kind {
	case "thermal":
		points := m.pointsMatching(func(p series.Point) bool {
			return p.Kind == "temperature" && p.Value >= 1 && p.Value < 200
		})
		return m.seriesFromPoints(points, 6), "THERMALS", "°C"
	case "power":
		points := m.pointsMatching(func(p series.Point) bool {
			return p.Kind == "power" && p.Value > 0 && (p.Symbol == "W" || strings.Contains(p.Name, "power"))
		})
		return m.seriesFromPoints(points, 6), "POWER", "W"
	default:
		defs := []struct{ name, label string }{
			{"disk_read_bytes_per_second", "disk read"},
			{"disk_written_bytes_per_second", "disk write"},
			{"network_rx_bytes_per_second", "network down"},
			{"network_tx_bytes_per_second", "network up"},
		}
		out := make([]graphSeries, 0, len(defs))
		for i, def := range defs {
			if values := m.aggregateHistory(def.name); len(values) > 0 {
				out = append(out, graphSeries{name: def.label, values: values, color: graphPalette[i], unit: "B/s"})
			}
		}
		return out, "I/O", "B/s"
	}
}

func (m model) pointsMatching(match func(series.Point) bool) []series.Point {
	var out []series.Point
	for _, point := range m.values {
		if match(point) && len(m.history[point.Key()]) > 0 {
			out = append(out, point)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	return out
}

func (m model) seriesFromPoints(points []series.Point, limit int) []graphSeries {
	if len(points) > limit {
		points = points[:limit]
	}
	out := make([]graphSeries, 0, len(points))
	for i, point := range points {
		out = append(out, graphSeries{
			name: graphSeriesName(point), values: m.history[point.Key()],
			color: graphPalette[i%len(graphPalette)], unit: point.Symbol,
		})
	}
	return out
}

func (m model) aggregateHistory(name string) []float64 {
	var histories [][]float64
	longest := 0
	for key, point := range m.values {
		if point.Name == name && len(m.history[key]) > 0 {
			histories = append(histories, m.history[key])
			longest = max(longest, len(m.history[key]))
		}
	}
	if longest == 0 {
		return nil
	}
	out := make([]float64, longest)
	for _, history := range histories {
		offset := longest - len(history)
		for i, value := range history {
			out[offset+i] += value
		}
	}
	return out
}

func graphSeriesName(point series.Point) string {
	for _, key := range []string{"sensor", "device", "name", "card", "chip", "type", "supply"} {
		if point.Labels[key] != "" {
			return point.Labels[key]
		}
	}
	return strings.TrimSuffix(strings.ReplaceAll(point.Name, "_", " "), " celsius")
}

func graphLegend(series []graphSeries, width int) string {
	parts := make([]string, 0, len(series))
	for _, s := range series {
		latest := s.values[len(s.values)-1]
		label := truncate(s.name, 18) + " " + formatValue(latest, s.unit)
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color(hexColor(s.color))).Render("● "+label))
	}
	return " " + truncate(strings.Join(parts, "   "), width-1)
}

func brailleGraph(data []graphSeries, width, height int) string {
	axisWidth := 13
	plotWidth := max(8, width-axisWidth)
	plotHeight := max(4, height-1)
	pixelW, pixelH := plotWidth*2, plotHeight*4
	lo, hi := graphRange(data)
	dots := make([][]byte, plotHeight)
	colors := make([][]int, plotHeight)
	for y := range dots {
		dots[y] = make([]byte, plotWidth)
		colors[y] = make([]int, plotWidth)
		for x := range colors[y] {
			colors[y][x] = -1
		}
	}
	for si, s := range data {
		values := resample(s.values, pixelW)
		lastX, lastY := -1, -1
		for x, v := range values {
			y := int(math.Round((hi - v) / (hi - lo) * float64(pixelH-1)))
			y = min(max(y, 0), pixelH-1)
			if lastX >= 0 {
				drawBrailleLine(dots, colors, lastX, lastY, x, y, si)
			} else {
				setBrailleDot(dots, colors, x, y, si)
			}
			lastX, lastY = x, y
		}
	}
	var b strings.Builder
	for row := 0; row < plotHeight; row++ {
		value := hi - (hi-lo)*float64(row)/float64(max(1, plotHeight-1))
		b.WriteString(lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("%11s │", graphAxis(value, data[0].unit))))
		for col, bits := range dots[row] {
			r := rune(0x2800 + int(bits))
			if bits == 0 {
				r = '·'
			}
			ci := colors[row][col]
			c := lipgloss.Color("#273249")
			if ci >= 0 {
				c = lipgloss.Color(hexColor(data[ci%len(data)].color))
			}
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
		}
		if row+1 < plotHeight {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func miniBrailleGraph(data []graphSeries, width, height int) string {
	width, height = max(4, width), max(1, height)
	if len(data) == 0 {
		lines := make([]string, height)
		for i := range lines {
			lines[i] = strings.Repeat(" ", width)
		}
		label := "NO TELEMETRY"
		if width < len(label)+2 {
			label = "NO DATA"
		}
		left := max(0, (width-len(label))/2)
		lines[height/2] = strings.Repeat(" ", left) + label
		well := lipgloss.NewStyle().Foreground(muted).Background(lipgloss.Color("#05070C")).Width(width)
		for i := range lines {
			lines[i] = well.Render(lines[i])
		}
		return strings.Join(lines, "\n")
	}
	pixelW, pixelH := width*2, height*4
	lo, hi := graphRange(data)
	dots := make([][]byte, height)
	colors := make([][]int, height)
	for y := range dots {
		dots[y] = make([]byte, width)
		colors[y] = make([]int, width)
		for x := range colors[y] {
			colors[y][x] = -1
		}
	}
	for si, s := range data {
		values := resample(s.values, pixelW)
		lastX, lastY := -1, -1
		for x, value := range values {
			y := int(math.Round((hi - value) / (hi - lo) * float64(pixelH-1)))
			y = min(max(y, 0), pixelH-1)
			if lastX >= 0 {
				drawBrailleLine(dots, colors, lastX, lastY, x, y, si)
			} else {
				setBrailleDot(dots, colors, x, y, si)
			}
			lastX, lastY = x, y
		}
	}
	var b strings.Builder
	for row := range dots {
		for col, bits := range dots[row] {
			r := ' '
			if bits != 0 {
				r = rune(0x2800 + int(bits))
			}
			ci := colors[row][col]
			c := lipgloss.Color("#273249")
			if ci >= 0 {
				c = lipgloss.Color(hexColor(data[ci%len(data)].color))
			}
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
		}
		if row+1 < height {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m model) cardGraph(data []graphSeries, width, height, id int, placement graphPlacement) string {
	_, _, _ = m, id, placement
	return miniBrailleGraph(data, width, height)
}

func stableGraphID(value string) int {
	return int(crc32.ChecksumIEEE([]byte(value)))
}

var brailleBits = [4][2]byte{{1, 8}, {2, 16}, {4, 32}, {64, 128}}

func setBrailleDot(dots [][]byte, colors [][]int, x, y, series int) {
	row, col := y/4, x/2
	if row < 0 || row >= len(dots) || col < 0 || col >= len(dots[row]) {
		return
	}
	dots[row][col] |= brailleBits[y%4][x%2]
	colors[row][col] = series
}
func drawBrailleLine(dots [][]byte, colors [][]int, x0, y0, x1, y1, series int) {
	dx := abs(x1 - x0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	dy := -abs(y1 - y0)
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		setBrailleDot(dots, colors, x0, y0, series)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func graphRange(data []graphSeries) (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range data {
		for _, v := range s.values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			lo = math.Min(lo, v)
			hi = math.Max(hi, v)
		}
	}
	if math.IsInf(lo, 1) {
		return 0, 1
	}
	if data[0].unit == "°C" {
		lo = math.Max(0, lo-5)
		hi += 5
	} else if lo > 0 {
		lo = 0
	}
	if hi <= lo {
		hi = lo + 1
	}
	hi += (hi - lo) * .08
	return lo, hi
}
func resample(values []float64, n int) []float64 {
	if len(values) == 0 || n <= 0 {
		return nil
	}
	out := make([]float64, n)
	for i := range out {
		idx := int(math.Round(float64(i) * float64(len(values)-1) / float64(max(1, n-1))))
		out[i] = values[idx]
	}
	return out
}
func graphAxis(v float64, unit string) string {
	if unit == "B/s" {
		return bytesRate(v)
	}
	return formatValue(v, unit)
}
func graphColor(kind string) lipgloss.Color {
	if kind == "thermal" {
		return red
	}
	if kind == "power" {
		return orange
	}
	return green
}
func hexColor(c color.RGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
