package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type statsData struct {
	Uptime      int64             `json:"uptime_s"`
	MsgCount    uint64            `json:"msg_count"`
	MsgRate     float64           `json:"msg_rate"`
	CrcPassRate float64           `json:"crc_pass_rate"`
	DropCount   uint64            `json:"drop_count"`
	ICAOCount   int               `json:"icao_count"`
	Aircraft    int               `json:"aircraft_count"`
	Overflow    bool              `json:"overflow"`
	Debug       map[string]uint32 `json:"debug"`
}

type sample struct {
	At              time.Time
	MsgRate         float64
	ValidRate       float64
	DropRate        float64
	InvalidDFRate   float64
	ValidRatio      float64
	CrcPreDetRatio  float64
	CrcPrePassRatio float64
	DropMsgRatio    float64
	Aircraft        float64
	IQ75Rate        float64
	IQ87Rate        float64
	NearrailRate    float64
	PwrSatRate      float64
}

type pollResult struct {
	At    time.Time
	Stats *statsData
	Err   error
}

type controlResult struct {
	Kind  string
	Value string
	Err   error
}

type model struct {
	client   *http.Client
	baseURL  string
	interval time.Duration
	history  int

	width  int
	height int

	last          *statsData
	prev          *statsData
	prevAt        time.Time
	samples       []sample
	lastErr       error
	controlStatus string
	polling       bool
}

var (
	appStyle = lipgloss.NewStyle().Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	okStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("42"))

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("220"))

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	statKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("248"))

	statValStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252"))
)

func main() {
	baseURL := flag.String("base-url", "http://planewatcher.local:8080", "plane-feeder base URL")
	interval := flag.Duration("interval", 2*time.Second, "poll interval")
	history := flag.Int("history", 90, "number of samples to keep in charts")
	timeout := flag.Duration("timeout", 1500*time.Millisecond, "HTTP request timeout")
	flag.Parse()

	if *history < 16 {
		*history = 16
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be > 0")
		os.Exit(2)
	}

	m := model{
		client:   &http.Client{Timeout: *timeout},
		baseURL:  strings.TrimRight(*baseURL, "/"),
		interval: *interval,
		history:  *history,
		polling:  true,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pollCmd(), everyCmd(m.interval))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "c":
			m.samples = nil
			m.prev = nil
			m.prevAt = time.Time{}
			return m, nil
		case "]":
			return m, m.adjustQuietCmd(1)
		case "[":
			return m, m.adjustQuietCmd(-1)
		case "}":
			return m, m.adjustSnrCmd(1)
		case "{":
			return m, m.adjustSnrCmd(-1)
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case pollResult:
		m.polling = false
		if msg.Err != nil {
			m.lastErr = msg.Err
			return m, nil
		}

		m.last = msg.Stats
		m.lastErr = nil
		if m.prev != nil && !m.prevAt.IsZero() {
			elapsed := msg.At.Sub(m.prevAt).Seconds()
			if elapsed > 0 {
				m.samples = append(m.samples, deriveSample(msg.Stats, m.prev, elapsed, msg.At))
				if len(m.samples) > m.history {
					m.samples = m.samples[len(m.samples)-m.history:]
				}
			}
		}
		m.prev = msg.Stats
		m.prevAt = msg.At
		return m, nil
	case controlResult:
		if msg.Err != nil {
			m.controlStatus = fmt.Sprintf("%s failed: %v", msg.Kind, msg.Err)
		} else {
			m.controlStatus = fmt.Sprintf("%s -> %s", msg.Kind, msg.Value)
		}
		if m.polling {
			return m, nil
		}
		m.polling = true
		return m, m.pollCmd()
	case tickMsg:
		if m.polling {
			return m, everyCmd(m.interval)
		}
		m.polling = true
		return m, tea.Batch(m.pollCmd(), everyCmd(m.interval))
	}

	return m, nil
}

type tickMsg struct{}

func everyCmd(interval time.Duration) tea.Cmd {
	return tea.Every(interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) View() string {
	if m.width == 0 {
		return "initialising..."
	}

	bodyWidth := max(60, m.width-4)
	header := m.renderHeader(bodyWidth)

	if m.last == nil {
		return appStyle.Width(bodyWidth).Render(header + "\n\nwaiting for first sample...")
	}

	top := lipgloss.JoinHorizontal(
		lipgloss.Top,
		panelStyle.Width(max(28, bodyWidth/2-1)).Render(m.renderSummary()),
		panelStyle.Width(max(28, bodyWidth-bodyWidth/2-1)).Render(m.renderFrontend()),
	)

	mid := lipgloss.JoinHorizontal(
		lipgloss.Top,
		panelStyle.Width(max(28, bodyWidth/2-1)).Render(m.renderChart("Throughput", []metricSeries{
			{label: "Msg/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.MsgRate }), color: lipgloss.Color("39")},
			{label: "Valid/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.ValidRate }), color: lipgloss.Color("42")},
			{label: "Drop/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.DropRate }), color: lipgloss.Color("203")},
		})),
		panelStyle.Width(max(28, bodyWidth-bodyWidth/2-1)).Render(m.renderChart("Quality", []metricSeries{
			{label: "Invalid DF/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.InvalidDFRate }), color: lipgloss.Color("220")},
			{label: "Valid:Invalid", unit: "", values: seriesOf(m.samples, func(s sample) float64 { return s.ValidRatio }), color: lipgloss.Color("45")},
			{label: "CRC:PreDet", unit: "", values: seriesOf(m.samples, func(s sample) float64 { return s.CrcPreDetRatio }), color: lipgloss.Color("81")},
			{label: "CRC:PrePass", unit: "", values: seriesOf(m.samples, func(s sample) float64 { return s.CrcPrePassRatio }), color: lipgloss.Color("117")},
			{label: "Aircraft", unit: "", values: seriesOf(m.samples, func(s sample) float64 { return s.Aircraft }), color: lipgloss.Color("111")},
		})),
	)

	bot := panelStyle.Width(bodyWidth).Render(m.renderChart("Frontend Headroom", []metricSeries{
		{label: "IQ75/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.IQ75Rate }), color: lipgloss.Color("214")},
		{label: "IQ87.5/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.IQ87Rate }), color: lipgloss.Color("208")},
		{label: "Nearrail/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.NearrailRate }), color: lipgloss.Color("196")},
		{label: "PwrSat/s", unit: "/s", values: seriesOf(m.samples, func(s sample) float64 { return s.PwrSatRate }), color: lipgloss.Color("199")},
	}))

	return appStyle.Width(bodyWidth).Render(lipgloss.JoinVertical(lipgloss.Left, header, top, mid, bot))
}

func (m model) renderHeader(width int) string {
	status := okStyle.Render("ok")
	if m.lastErr != nil {
		status = errStyle.Render(m.lastErr.Error())
	}
	left := titleStyle.Render("Plane Watcher Tuning Dashboard")
	right := dimStyle.Render(fmt.Sprintf("poll %s  history %d  q quit  c clear", m.interval, m.history))
	keys := dimStyle.Render("[/ ] quiet  {/ } snr")
	line := lipgloss.PlaceHorizontal(width, lipgloss.Left, left)
	meta := lipgloss.PlaceHorizontal(width, lipgloss.Left, fmt.Sprintf("status %s", status))
	control := dimStyle.Render("control idle")
	if m.controlStatus != "" {
		control = dimStyle.Render("control " + m.controlStatus)
	}
	return strings.Join([]string{line, right, keys, meta, control}, "\n")
}

func (m model) renderSummary() string {
	s := m.last
	latest := latestSample(m.samples)
	return strings.Join([]string{
		kv("Msgs", fmt.Sprintf("%d", s.MsgCount)),
		kv("Drops", fmt.Sprintf("%d", s.DropCount)),
		kv("Valid:Invalid", formatRatio(latest.ValidRatio)),
		kv("CRC:PreDet", formatRatio(latest.CrcPreDetRatio)),
		kv("CRC:PrePass", formatRatio(latest.CrcPrePassRatio)),
		kv("Drops:Msgs", formatRatio(latest.DropMsgRatio)),
		kv("Aircraft", fmt.Sprintf("%d", s.Aircraft)),
		kv("ICAOs", fmt.Sprintf("%d", s.ICAOCount)),
		kv("Uptime", fmt.Sprintf("%ds", s.Uptime)),
		kv("Overflow", boolWord(s.Overflow)),
		kv("Quiet/SNR", fmt.Sprintf("%d / %d", debugValue(s, "quiet_score_shift"), debugValue(s, "snr_ratio_shift"))),
		kv("Holdoff", fmt.Sprintf("%d", debugValue(s, "holdoff"))),
	}, "\n")
}

func (m model) renderFrontend() string {
	s := m.last
	headroomState := okStyle.Render("clean")
	latest := latestSample(m.samples)
	if latest.NearrailRate > 0 || latest.PwrSatRate > 0 {
		headroomState = errStyle.Render("overdriven")
	} else if latest.IQ75Rate > 0 || latest.IQ87Rate > 0 {
		headroomState = warnStyle.Render("hot")
	}

	return strings.Join([]string{
		kv("Headroom", headroomState),
		kv("CRC pass", fmt.Sprintf("%d", debugValue(s, "crc_pass_ct"))),
		kv("CRC exhaust", fmt.Sprintf("%d", debugValue(s, "crc_exhaust_ct"))),
		kv("Invalid DF", fmt.Sprintf("%d", debugValue(s, "invalid_df_ct"))),
		kv("Pre-det", fmt.Sprintf("%d", debugValue(s, "pre_det_ct"))),
		kv("Pre-pass", fmt.Sprintf("%d", debugValue(s, "pre_pass_ct"))),
		kv("IQ75", fmt.Sprintf("%d", debugValue(s, "raw_iq_75pct_ct"))),
		kv("IQ87.5", fmt.Sprintf("%d", debugValue(s, "raw_iq_87p5pct_ct"))),
		kv("Nearrail", fmt.Sprintf("%d", debugValue(s, "raw_iq_nearrail_ct"))),
		kv("Pwr sat", fmt.Sprintf("%d", debugValue(s, "raw_power_sat_ct"))),
	}, "\n")
}

type metricSeries struct {
	label  string
	unit   string
	values []float64
	color  lipgloss.Color
}

func (m model) renderChart(title string, series []metricSeries) string {
	lines := []string{titleStyle.Render(title)}
	for _, row := range series {
		current := lastValue(row.values)
		minVal, maxVal := minMax(row.values)
		barWidth := max(20, min(56, m.width-34))
		lines = append(lines,
			fmt.Sprintf(
				"%-11s %8.1f%-2s %s %s",
				row.label,
				current,
				row.unit,
				colorizeSparkline(sparkline(row.values, barWidth), row.color),
				dimStyle.Render(fmt.Sprintf("min %.1f  max %.1f", minVal, maxVal)),
			),
		)
	}
	return strings.Join(lines, "\n")
}

func (m model) pollCmd() tea.Cmd {
	client := m.client
	baseURL := m.baseURL
	return func() tea.Msg {
		now := time.Now()
		stats, err := fetchStats(client, baseURL)
		return pollResult{At: now, Stats: stats, Err: err}
	}
}

func (m model) adjustQuietCmd(delta int) tea.Cmd {
	if m.last == nil {
		return func() tea.Msg { return controlResult{Kind: "quiet", Err: fmt.Errorf("no stats yet")} }
	}
	cur := int(debugValue(m.last, "quiet_score_shift"))
	next := clampInt(cur+delta, 0, 7)
	return m.postValueCmd("/api/detector/quiet-score-shift", "quiet", next)
}

func (m model) adjustSnrCmd(delta int) tea.Cmd {
	if m.last == nil {
		return func() tea.Msg { return controlResult{Kind: "snr", Err: fmt.Errorf("no stats yet")} }
	}
	cur := int(debugValue(m.last, "snr_ratio_shift"))
	next := clampInt(cur+delta, 0, 7)
	return m.postValueCmd("/api/detector/snr-ratio-shift", "snr", next)
}

func (m model) postValueCmd(path, kind string, value int) tea.Cmd {
	client := m.client
	baseURL := m.baseURL
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]uint32{"value": uint32(value)})
		req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(body))
		if err != nil {
			return controlResult{Kind: kind, Err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return controlResult{Kind: kind, Err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return controlResult{Kind: kind, Err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return controlResult{Kind: kind, Value: fmt.Sprintf("%d", value)}
	}
}

func fetchStats(client *http.Client, baseURL string) (*statsData, error) {
	resp, err := client.Get(baseURL + "/api/stats?debug=1")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/stats?debug=1: status %d", resp.StatusCode)
	}

	var stats statsData
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	if stats.Debug == nil {
		stats.Debug = map[string]uint32{}
	}
	return &stats, nil
}

func deriveSample(cur, prev *statsData, elapsed float64, now time.Time) sample {
	validRate := deltaRate(debugValue(cur, "crc_pass_ct"), debugValue(prev, "crc_pass_ct"), elapsed, cur.CrcPassRate)
	invalidRate := deltaRate(debugValue(cur, "invalid_df_ct"), debugValue(prev, "invalid_df_ct"), elapsed, 0)
	preDetRate := deltaRate(debugValue(cur, "pre_det_ct"), debugValue(prev, "pre_det_ct"), elapsed, 0)
	prePassRate := deltaRate(debugValue(cur, "pre_pass_ct"), debugValue(prev, "pre_pass_ct"), elapsed, 0)
	msgRate := deltaRate(cur.MsgCount, prev.MsgCount, elapsed, cur.MsgRate)
	dropRate := deltaRate(cur.DropCount, prev.DropCount, elapsed, 0)
	return sample{
		At:              now,
		MsgRate:         msgRate,
		ValidRate:       validRate,
		DropRate:        dropRate,
		InvalidDFRate:   invalidRate,
		ValidRatio:      safeRatio(validRate, invalidRate),
		CrcPreDetRatio:  safeRatio(validRate, preDetRate),
		CrcPrePassRatio: safeRatio(validRate, prePassRate),
		DropMsgRatio:    safeRatio(dropRate, msgRate),
		Aircraft:        float64(cur.Aircraft),
		IQ75Rate:        deltaRate(debugValue(cur, "raw_iq_75pct_ct"), debugValue(prev, "raw_iq_75pct_ct"), elapsed, 0),
		IQ87Rate:        deltaRate(debugValue(cur, "raw_iq_87p5pct_ct"), debugValue(prev, "raw_iq_87p5pct_ct"), elapsed, 0),
		NearrailRate:    deltaRate(debugValue(cur, "raw_iq_nearrail_ct"), debugValue(prev, "raw_iq_nearrail_ct"), elapsed, 0),
		PwrSatRate:      deltaRate(debugValue(cur, "raw_power_sat_ct"), debugValue(prev, "raw_power_sat_ct"), elapsed, 0),
	}
}

func kv(key, value string) string {
	return statKeyStyle.Render(key+":") + " " + statValStyle.Render(value)
}

func boolWord(v bool) string {
	if v {
		return warnStyle.Render("yes")
	}
	return okStyle.Render("no")
}

func deltaRate[T ~uint32 | ~uint64](cur, prev T, elapsed float64, fallback float64) float64 {
	if cur >= prev && elapsed > 0 {
		return float64(cur-prev) / elapsed
	}
	return fallback
}

func debugValue(stats *statsData, key string) uint32 {
	if stats == nil || stats.Debug == nil {
		return 0
	}
	return stats.Debug[key]
}

func seriesOf(samples []sample, f func(sample) float64) []float64 {
	out := make([]float64, 0, len(samples))
	for _, s := range samples {
		out = append(out, f(s))
	}
	return out
}

func lastValue(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	return vals[len(vals)-1]
}

func latestSample(samples []sample) sample {
	if len(samples) == 0 {
		return sample{}
	}
	return samples[len(samples)-1]
}

func safeRatio(valid, invalid float64) float64 {
	if invalid <= 0 {
		if valid <= 0 {
			return 0
		}
		return valid
	}
	return valid / invalid
}

func formatRatio(v float64) string {
	if v <= 0 {
		return "0.00:1"
	}
	return fmt.Sprintf("%.2f:1", v)
}

func minMax(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	minVal, maxVal := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	return minVal, maxVal
}

func sparkline(vals []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(vals) == 0 {
		return strings.Repeat(" ", width)
	}

	resampled := resample(vals, width)
	_, maxVal := minMax(resampled)
	if maxVal <= 0 {
		return strings.Repeat("▁", width)
	}

	levels := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	out := make([]rune, width)
	for i, v := range resampled {
		if v < 0 {
			v = 0
		}
		idx := int(math.Round((v / maxVal) * float64(len(levels)-1)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(levels) {
			idx = len(levels) - 1
		}
		out[i] = levels[idx]
	}
	return string(out)
}

func colorizeSparkline(line string, color lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(color).Render(line)
}

func resample(vals []float64, width int) []float64 {
	if len(vals) == width {
		return append([]float64(nil), vals...)
	}
	if len(vals) > width {
		step := float64(len(vals)) / float64(width)
		out := make([]float64, width)
		for i := range out {
			start := int(math.Floor(float64(i) * step))
			end := int(math.Floor(float64(i+1) * step))
			if end <= start {
				end = start + 1
			}
			if end > len(vals) {
				end = len(vals)
			}
			var maxVal float64
			for j := start; j < end; j++ {
				if j == start || vals[j] > maxVal {
					maxVal = vals[j]
				}
			}
			out[i] = maxVal
		}
		return out
	}

	out := make([]float64, width)
	if len(vals) == 1 {
		for i := range out {
			out[i] = vals[0]
		}
		return out
	}
	for i := range out {
		pos := float64(i) * float64(len(vals)-1) / float64(width-1)
		left := int(math.Floor(pos))
		right := int(math.Ceil(pos))
		if right >= len(vals) {
			right = len(vals) - 1
		}
		if left == right {
			out[i] = vals[left]
			continue
		}
		frac := pos - float64(left)
		out[i] = vals[left]*(1-frac) + vals[right]*frac
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
