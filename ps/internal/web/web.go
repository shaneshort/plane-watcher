package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/diag"
	"github.com/plane-watcher/plane-feeder/internal/gps"
	"github.com/plane-watcher/plane-feeder/internal/gpsmon"
	"github.com/plane-watcher/plane-feeder/internal/tracker"
)

var metricLabelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)

const uiBuildChip = "ui 2026.04.14c"

// Dashboard decode-health thresholds. The CRC-pass / message-rate ratio
// determines whether the "Decode" pill is rendered good/warn/bad; rates
// below MinDecodeRate are treated as idle (warn, not bad) to avoid
// flagging a quiet channel as a decode failure.
const (
	MinDecodeRate      = 5.0
	DecodeRatioGood    = 0.65
	DecodeRatioWarn    = 0.35
	YoungSystemUptimeS = 120
)

// GPS satellite signal thresholds. SNR values from u-blox report in
// dB-Hz; 50 is the bar used as "full scale" for the horizontal bar
// indicator, and the class thresholds split the bar between red/yellow/
// no-colour for weak / borderline / healthy respectively.
const (
	SatSNRFullScale    = 50.0
	SatSNRClassBadMax  = 20.0
	SatSNRClassWarnMax = 35.0
)

// haversineKm returns the great-circle distance in kilometres between two
// points specified in decimal degrees.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	lat1r := lat1 * math.Pi / 180
	lat2r := lat2 * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1r)*math.Cos(lat2r)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

//go:embed static
var staticFiles embed.FS

//go:embed templates/*.html
var templateFiles embed.FS

var pageShellTemplate = template.Must(template.ParseFS(templateFiles, "templates/*.html"))

type statView struct {
	Label string
	Value string
	Class string
}

type kvView struct {
	Key   string
	Value string
	Class string
}

type dropRowView struct {
	Label string
	Count string
	Sort  uint64
}

type recentFrameView struct {
	Seen        string
	Reason      string
	DF          string
	DerivedICAO string
	Payload     string
	Signal      string
}

type gpsSatelliteRowView struct {
	PRN      string
	GNSS     string
	Az       string
	El       string
	SNR      string
	SNRPct   int
	SNRClass string
	Used     bool
}

type heroMetaView struct {
	Label string
	Value string
}

type healthPillView struct {
	State  string
	Label  string
	Value  string
	Reason string
}

type dashboardAircraftRow struct {
	ICAO     string
	Callsign string
	Squawk   string
	Altitude string
	Distance string
	Lat      string
	Lon      string
	Signal   string
	Messages string
	Age      string
	Pinned   bool
}

type dashboardWatchCard struct {
	ICAO     string
	Title    string
	Subtitle string
	Signal   string
	Messages string
	Age      string
	Visible  bool
}

type pageTemplateData struct {
	PageTitle  string
	ActivePage string
	BuildChip  string
	Content    template.HTML
}

type StatsData struct {
	Uptime        int64             `json:"uptime_s"`
	MsgCount      uint64            `json:"msg_count"`
	MsgRate       float64           `json:"msg_rate"`
	CrcPassRate   float64           `json:"crc_pass_rate"`
	DropCount     uint64            `json:"drop_count"`
	ICAOCount     int               `json:"icao_count"`
	AircraftCount int               `json:"aircraft_count"`
	ClientCount   int               `json:"client_count"`
	PPSCount      uint32            `json:"pps_count"`
	Overflow      bool              `json:"overflow"`
	GpsSync       bool              `json:"gps_sync"`
	OscillatorPPM float64           `json:"oscillator_ppm"`
	Carryover     int64             `json:"carryover"`
	AvgCarryover  float64           `json:"avg_carryover"`
	CarryoverMin  int64             `json:"carryover_min"`
	CarryoverMax  int64             `json:"carryover_max"`
	PpsTickRate   uint64            `json:"pps_interval_ticks"`
	SkippedEdges  uint64            `json:"skipped_edges"`
	Debug         map[string]uint32 `json:"debug,omitempty"`
}

type StatsProvider interface {
	Stats(debug bool) StatsData
}

type DetectorConfig interface {
	SetQuietScoreShift(val uint32) error
	SetSnrRatioShift(val uint32) error
	SetHoldoff(val uint32) error
}

type RejectedFrameProvider interface {
	Snapshot(limit int) diag.RejectedFrameSummary
}

// GPSProvider is the interface the GPS dashboard uses to read the
// latest GNSS+PPS snapshot published by the gpsmon.Collector. It
// intentionally returns a typed *gpsmon.Snapshot rather than anything
// more generic — the web server and gpsmon evolve together, and the
// extra indirection would only buy us flexibility we don't need.
type GPSProvider interface {
	Snapshot() *gpsmon.Snapshot
	History() []gpsmon.Sample
}

type Server struct {
	tracker  *tracker.Tracker
	stats    StatsProvider
	detector DetectorConfig
	rejected RejectedFrameProvider
	gps      GPSProvider
	listener net.Listener
}

type aircraftResponse struct {
	ICAO     string   `json:"icao"`
	Callsign string   `json:"callsign,omitempty"`
	Altitude int64    `json:"altitude,omitempty"`
	Lat      float64  `json:"lat,omitempty"`
	Lon      float64  `json:"lon,omitempty"`
	Distance *float64 `json:"distance_km,omitempty"`
	Squawk   string   `json:"squawk,omitempty"`
	Signal   uint8    `json:"signal"`
	Seen     string   `json:"seen"`
	Messages uint64   `json:"messages"`
}

// New constructs a Server. Any provider may be nil; handlers that depend
// on a nil provider return 503. The GPS provider is the latest addition:
// when nil, /api/gps and /gps both still route but respond with a
// "GPS monitor not available" payload instead of 404.
func New(
	t *tracker.Tracker,
	sp StatsProvider,
	dc DetectorConfig,
	rp RejectedFrameProvider,
	gp GPSProvider,
) *Server {
	return &Server{
		tracker:  t,
		stats:    sp,
		detector: dc,
		rejected: rp,
		gps:      gp,
	}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/aircraft", s.handleAircraft)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/drops", s.handleDrops)
	mux.HandleFunc("GET /api/gps", s.handleGNSS)
	mux.HandleFunc("GET /api/gps/history", s.handleGNSSHistory)
	mux.HandleFunc("GET /partials/gps/details", s.handleGPSDetailsPartial)
	mux.HandleFunc("GET /partials/receiver/state", s.handleReceiverStatePartial)
	mux.HandleFunc("GET /partials/diagnostics/summary", s.handleDiagnosticsSummaryPartial)
	mux.HandleFunc("GET /partials/diagnostics/drops", s.handleDiagnosticsDropsPartial)
	mux.HandleFunc("GET /partials/dashboard/state", s.handleDashboardStatePartial)
	mux.HandleFunc("GET /partials/dashboard/aircraft", s.handleDashboardAircraftPartial)
	mux.HandleFunc("GET /partials/dashboard/watchlist", s.handleDashboardWatchlistPartial)
	mux.HandleFunc("POST /api/detector/quiet-score-shift", s.handleSetQuietScoreShift)
	mux.HandleFunc("POST /api/detector/snr-ratio-shift", s.handleSetSnrRatioShift)
	mux.HandleFunc("POST /api/detector/holdoff", s.handleSetHoldoff)
	staticSub, _ := fs.Sub(staticFiles, "static")
	assetsSub, _ := fs.Sub(staticSub, "assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assetsSub)))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "index.html", "plane-watcher", "dashboard")
	})
	mux.HandleFunc("GET /index.html", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "index.html", "plane-watcher", "dashboard")
	})
	mux.HandleFunc("GET /gps", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "gps.html", "plane-watcher — GPS", "gps")
	})
	mux.HandleFunc("GET /gps.html", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "gps.html", "plane-watcher — GPS", "gps")
	})
	mux.HandleFunc("GET /receiver", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "receiver.html", "plane-watcher — Receiver", "receiver")
	})
	mux.HandleFunc("GET /receiver.html", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "receiver.html", "plane-watcher — Receiver", "receiver")
	})
	mux.HandleFunc("GET /diagnostics", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "diagnostics.html", "plane-watcher — Diagnostics", "diagnostics")
	})
	mux.HandleFunc("GET /diagnostics.html", func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, staticSub, "diagnostics.html", "plane-watcher — Diagnostics", "diagnostics")
	})
	return mux
}

// readPageFragment reads a static page file and returns it verbatim as
// HTML content. The static files are stored as pure body fragments —
// no <html>, <head>, topbar, or nav — because the shared base.html
// shell provides all of that when renderPage assembles the final page.
func readPageFragment(staticSub fs.FS, filename string) (template.HTML, error) {
	pageBytes, err := fs.ReadFile(staticSub, filename)
	if err != nil {
		return "", err
	}
	return template.HTML(pageBytes), nil
}

func (s *Server) renderPage(w http.ResponseWriter, staticSub fs.FS, filename, title, activePage string) {
	content, err := readPageFragment(staticSub, filename)
	if err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageShellTemplate.ExecuteTemplate(w, "base", pageTemplateData{
		PageTitle:  title,
		ActivePage: activePage,
		BuildChip:  uiBuildChip,
		Content:    content,
	}); err != nil {
		http.Error(w, "page render failed", http.StatusInternalServerError)
	}
}

func executeTemplateToString(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := pageShellTemplate.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("partial render failed: template=%s err=%v", name, err)
		return "", err
	}
	return buf.String(), nil
}

func fmtMaybeFloat(f float64, suffix string) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "-"
	}
	return fmt.Sprintf("%.1f%s", f, suffix)
}

func ageLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		return "0s"
	}
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm", secs/60)
}

func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	s.listener = ln
	go http.Serve(ln, s.handler())
	return nil
}

func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	debug := r.URL.Query().Get("debug") == "1"
	data := s.stats.Stats(debug)
	data.AircraftCount = s.tracker.Count()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleAircraft(w http.ResponseWriter, r *http.Request) {
	snap := s.tracker.Snapshot()
	refLat, refLon := s.tracker.RefPos()
	hasRef := refLat != 0 || refLon != 0
	list := make([]aircraftResponse, 0, len(snap))
	for _, ac := range snap {
		resp := aircraftResponse{
			ICAO:     fmt.Sprintf("%06X", ac.ICAO),
			Callsign: ac.Callsign,
			Altitude: ac.Altitude,
			Lat:      ac.Lat,
			Lon:      ac.Lon,
			Squawk:   ac.Squawk,
			Signal:   ac.Signal,
			Seen:     ac.Seen.Format(time.RFC3339Nano),
			Messages: ac.Messages,
		}
		if hasRef && ac.Lat != 0 && ac.Lon != 0 {
			d := haversineKm(refLat, refLon, ac.Lat, ac.Lon)
			d = math.Round(d*10) / 10
			resp.Distance = &d
		}
		list = append(list, resp)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ICAO < list[j].ICAO
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func sanitizeMetricName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func escapeLabelValue(v string) string {
	return metricLabelEscaper.Replace(v)
}

func writeMetricHelp(buf *bytes.Buffer, name, help, metricType string) {
	fmt.Fprintf(buf, "# HELP %s %s\n", name, help)
	fmt.Fprintf(buf, "# TYPE %s %s\n", name, metricType)
}

func writeGauge(buf *bytes.Buffer, name string, value any) {
	fmt.Fprintf(buf, "%s %v\n", name, value)
}

func writeGaugeWithLabels(buf *bytes.Buffer, name string, labels map[string]string, value any) {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteString(name)
	if len(keys) > 0 {
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			fmt.Fprintf(buf, `%s="%s"`, sanitizeMetricName(k), escapeLabelValue(labels[k]))
		}
		buf.WriteByte('}')
	}
	fmt.Fprintf(buf, " %v\n", value)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		http.Error(w, "stats provider unavailable", http.StatusServiceUnavailable)
		return
	}

	stats := s.stats.Stats(true)
	if s.tracker != nil {
		stats.AircraftCount = s.tracker.Count()
	}
	var buf bytes.Buffer

	writeMetricHelp(&buf, "plane_watcher_uptime_seconds", "Process uptime in seconds.", "gauge")
	writeGauge(&buf, "plane_watcher_uptime_seconds", stats.Uptime)

	writeMetricHelp(&buf, "plane_watcher_msg_count_total", "Forwarded messages count.", "counter")
	writeGauge(&buf, "plane_watcher_msg_count_total", stats.MsgCount)
	writeMetricHelp(&buf, "plane_watcher_drop_count_total", "Dropped messages count.", "counter")
	writeGauge(&buf, "plane_watcher_drop_count_total", stats.DropCount)
	writeMetricHelp(&buf, "plane_watcher_msg_rate_per_second", "Forwarded message rate.", "gauge")
	writeGauge(&buf, "plane_watcher_msg_rate_per_second", stats.MsgRate)
	writeMetricHelp(&buf, "plane_watcher_crc_pass_rate_per_second", "CRC pass rate.", "gauge")
	writeGauge(&buf, "plane_watcher_crc_pass_rate_per_second", stats.CrcPassRate)
	writeMetricHelp(&buf, "plane_watcher_icao_count", "ICAO filter count.", "gauge")
	writeGauge(&buf, "plane_watcher_icao_count", stats.ICAOCount)
	writeMetricHelp(&buf, "plane_watcher_aircraft_count", "Tracked aircraft count.", "gauge")
	writeGauge(&buf, "plane_watcher_aircraft_count", stats.AircraftCount)
	writeMetricHelp(&buf, "plane_watcher_client_count", "Connected Beast clients.", "gauge")
	writeGauge(&buf, "plane_watcher_client_count", stats.ClientCount)
	writeMetricHelp(&buf, "plane_watcher_pps_count_total", "Observed PPS edges.", "counter")
	writeGauge(&buf, "plane_watcher_pps_count_total", stats.PPSCount)
	writeMetricHelp(&buf, "plane_watcher_overflow", "FIFO overflow flag.", "gauge")
	if stats.Overflow {
		writeGauge(&buf, "plane_watcher_overflow", 1)
	} else {
		writeGauge(&buf, "plane_watcher_overflow", 0)
	}
	writeMetricHelp(&buf, "plane_watcher_gps_sync", "GPS sync status.", "gauge")
	if stats.GpsSync {
		writeGauge(&buf, "plane_watcher_gps_sync", 1)
	} else {
		writeGauge(&buf, "plane_watcher_gps_sync", 0)
	}

	writeMetricHelp(&buf, "plane_watcher_oscillator_ppm", "Oscillator error in ppm.", "gauge")
	writeGauge(&buf, "plane_watcher_oscillator_ppm", stats.OscillatorPPM)
	writeMetricHelp(&buf, "plane_watcher_pps_carryover_ticks", "Current PPS carryover in ticks.", "gauge")
	writeGauge(&buf, "plane_watcher_pps_carryover_ticks", stats.Carryover)
	writeMetricHelp(&buf, "plane_watcher_pps_avg_carryover_ticks", "Average PPS carryover in ticks.", "gauge")
	writeGauge(&buf, "plane_watcher_pps_avg_carryover_ticks", stats.AvgCarryover)
	writeMetricHelp(&buf, "plane_watcher_pps_carryover_min_ticks", "Minimum PPS carryover in ticks.", "gauge")
	writeGauge(&buf, "plane_watcher_pps_carryover_min_ticks", stats.CarryoverMin)
	writeMetricHelp(&buf, "plane_watcher_pps_carryover_max_ticks", "Maximum PPS carryover in ticks.", "gauge")
	writeGauge(&buf, "plane_watcher_pps_carryover_max_ticks", stats.CarryoverMax)
	writeMetricHelp(&buf, "plane_watcher_pps_interval_ticks", "Observed PPS interval in decoder ticks.", "gauge")
	writeGauge(&buf, "plane_watcher_pps_interval_ticks", stats.PpsTickRate)
	writeMetricHelp(&buf, "plane_watcher_skipped_edges_total", "Skipped PPS edge count.", "counter")
	writeGauge(&buf, "plane_watcher_skipped_edges_total", stats.SkippedEdges)

	if len(stats.Debug) > 0 {
		keys := make([]string, 0, len(stats.Debug))
		for k := range stats.Debug {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeGauge(&buf, "plane_watcher_debug_"+sanitizeMetricName(strings.ToLower(k)), stats.Debug[k])
		}
		writeMetricHelp(&buf, "plane_watcher_ratio_crc_pass_per_som", "CRC pass divided by SOM count.", "gauge")
		if som := stats.Debug["som_ct"]; som > 0 {
			writeGauge(&buf, "plane_watcher_ratio_crc_pass_per_som", float64(stats.Debug["crc_pass_ct"])/float64(som))
		} else {
			writeGauge(&buf, "plane_watcher_ratio_crc_pass_per_som", 0)
		}
		writeMetricHelp(&buf, "plane_watcher_ratio_pre_det_per_pre_abs", "Preamble detects divided by absolute-threshold candidates.", "gauge")
		if preAbs := stats.Debug["pre_abs_ct"]; preAbs > 0 {
			writeGauge(&buf, "plane_watcher_ratio_pre_det_per_pre_abs", float64(stats.Debug["pre_det_ct"])/float64(preAbs))
		} else {
			writeGauge(&buf, "plane_watcher_ratio_pre_det_per_pre_abs", 0)
		}
	}

	if s.tracker != nil {
		snap := s.tracker.Snapshot()
		writeMetricHelp(&buf, "plane_watcher_aircraft_present", "Tracked aircraft presence.", "gauge")
		writeMetricHelp(&buf, "plane_watcher_aircraft_signal", "Tracked aircraft signal value.", "gauge")
		writeMetricHelp(&buf, "plane_watcher_aircraft_messages", "Tracked aircraft message count.", "gauge")
		writeMetricHelp(&buf, "plane_watcher_aircraft_seen_age_seconds", "Age of last seen timestamp for tracked aircraft.", "gauge")
		writeMetricHelp(&buf, "plane_watcher_aircraft_info", "Tracked aircraft metadata keyed by ICAO.", "gauge")
		now := time.Now()
		icaos := make([]uint32, 0, len(snap))
		for icao := range snap {
			icaos = append(icaos, icao)
		}
		sort.Slice(icaos, func(i, j int) bool { return icaos[i] < icaos[j] })
		for _, icao := range icaos {
			ac := snap[icao]
			labels := map[string]string{
				"icao": fmt.Sprintf("%06X", ac.ICAO),
			}
			writeGaugeWithLabels(&buf, "plane_watcher_aircraft_present", labels, 1)
			writeGaugeWithLabels(&buf, "plane_watcher_aircraft_signal", labels, ac.Signal)
			writeGaugeWithLabels(&buf, "plane_watcher_aircraft_messages", labels, ac.Messages)
			writeGaugeWithLabels(&buf, "plane_watcher_aircraft_seen_age_seconds", labels, now.Sub(ac.Seen).Seconds())
			infoLabels := map[string]string{"icao": labels["icao"]}
			if ac.Callsign != "" {
				infoLabels["callsign"] = ac.Callsign
			}
			if ac.Squawk != "" {
				infoLabels["squawk"] = ac.Squawk
			}
			writeGaugeWithLabels(&buf, "plane_watcher_aircraft_info", infoLabels, 1)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write(buf.Bytes())
}

// handleGNSS returns a merged GPS + PPS snapshot. The GPS side comes
// from the gpsmon.Collector (long-lived gpsd + UBX poll loop), the PPS
// side is lifted from the existing StatsProvider (which is what the
// dashboard already uses).
func (s *Server) handleGNSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339),
	}
	if s.gps != nil {
		resp["gps"] = s.gps.Snapshot()
	} else {
		resp["gps"] = nil
		resp["gps_error"] = "gps monitor not configured"
	}
	if s.stats != nil {
		st := s.stats.Stats(false)
		resp["pps"] = map[string]any{
			"gps_sync":           st.GpsSync,
			"oscillator_ppm":     st.OscillatorPPM,
			"carryover":          st.Carryover,
			"avg_carryover":      st.AvgCarryover,
			"carryover_min":      st.CarryoverMin,
			"carryover_max":      st.CarryoverMax,
			"pps_interval_ticks": st.PpsTickRate,
			"skipped_edges":      st.SkippedEdges,
			"pps_count":          st.PPSCount,
		}
	}
	json.NewEncoder(w).Encode(resp)
}

// handleGNSSHistory returns the rolling time-series samples maintained
// by gpsmon.Collector. Used by the GPS dashboard to render the PPS-ppm
// and used-sats charts.
func (s *Server) handleGNSSHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339),
	}
	if s.gps != nil {
		resp["samples"] = s.gps.History()
	} else {
		resp["samples"] = []gpsmon.Sample{}
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDrops(w http.ResponseWriter, r *http.Request) {
	limit := 32
	if s.rejected == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(diag.RejectedFrameSummary{ByDF: map[string]uint64{}, ByReason: map[string]uint64{}, Recent: []diag.RejectedFrame{}})
		return
	}
	data := s.rejected.Snapshot(limit)
	if data.ByDF == nil {
		data.ByDF = map[string]uint64{}
	}
	if data.ByReason == nil {
		data.ByReason = map[string]uint64{}
	}
	if data.Recent == nil {
		data.Recent = []diag.RejectedFrame{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleReceiverStatePartial(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		http.Error(w, "stats provider unavailable", http.StatusServiceUnavailable)
		return
	}
	d := s.stats.Stats(true)
	debug := d.Debug
	payload := struct {
		Stats    []statView
		Detector []kvView
	}{
		Stats: []statView{
			{Label: "Quiet Shift", Value: fmtUint32Debug(debug, "quiet_score_shift")},
			{Label: "SNR Shift", Value: fmtUint32Debug(debug, "snr_ratio_shift")},
			{Label: "Holdoff", Value: fmtUint32Debug(debug, "holdoff")},
		},
		Detector: []kvView{
			{Key: "quiet score shift", Value: fmtUint32Debug(debug, "quiet_score_shift")},
			{Key: "snr ratio shift", Value: fmtUint32Debug(debug, "snr_ratio_shift")},
			{Key: "holdoff", Value: fmtUint32Debug(debug, "holdoff")},
			{Key: "pre_abs_ct", Value: fmtUint32Debug(debug, "pre_abs_ct")},
			{Key: "pre_det_ct", Value: fmtUint32Debug(debug, "pre_det_ct")},
			{Key: "som_ct", Value: fmtUint32Debug(debug, "som_ct")},
			{Key: "crc_pass_ct", Value: fmtUint32Debug(debug, "crc_pass_ct")},
		},
	}
	body, err := executeTemplateToString("receiver_state", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleDiagnosticsSummaryPartial(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		http.Error(w, "stats provider unavailable", http.StatusServiceUnavailable)
		return
	}
	d := s.stats.Stats(true)
	debug := d.Debug
	counterKeys := make([]string, 0, len(debug))
	for k := range debug {
		counterKeys = append(counterKeys, k)
	}
	sort.Strings(counterKeys)
	counters := make([]kvView, 0, len(counterKeys))
	for _, k := range counterKeys {
		counters = append(counters, kvView{Key: k, Value: formatUint64(uint64(debug[k]))})
	}
	payload := struct {
		Stats    []statView
		Counters []kvView
		Timing   []kvView
	}{
		Stats: []statView{
			{Label: "pre_abs_ct", Value: fmtUint32Debug(debug, "pre_abs_ct")},
			{Label: "pre_det_ct", Value: fmtUint32Debug(debug, "pre_det_ct")},
			{Label: "som_ct", Value: fmtUint32Debug(debug, "som_ct")},
			{Label: "crc_pass_ct", Value: fmtUint32Debug(debug, "crc_pass_ct")},
			{Label: "invalid_df_ct", Value: fmtUint32Debug(debug, "invalid_df_ct")},
			{Label: "raw_power_max", Value: fmtUint32Debug(debug, "raw_power_max")},
			{Label: "overflow", Value: yesNo(d.Overflow), Class: ternary(d.Overflow, "bad", "")},
			{Label: "gps_sync", Value: yesNo(d.GpsSync), Class: ternary(d.GpsSync, "good", "bad")},
		},
		Counters: counters,
		Timing: []kvView{
			{Key: "gps sync", Value: yesNo(d.GpsSync), Class: ternary(d.GpsSync, "good", "bad")},
			{Key: "pps interval", Value: fmt.Sprintf("%s ticks", formatUint64(d.PpsTickRate))},
			{Key: "carryover (current)", Value: fmt.Sprintf("%d ticks", d.Carryover)},
			{Key: "carryover (avg)", Value: fmtMaybeFloat(d.AvgCarryover, " ticks")},
			{Key: "carryover (min)", Value: fmt.Sprintf("%d ticks", d.CarryoverMin)},
			{Key: "carryover (max)", Value: fmt.Sprintf("%d ticks", d.CarryoverMax)},
			{Key: "oscillator ppm", Value: fmtMaybeFloat(d.OscillatorPPM, " ppm")},
			{Key: "skipped edges", Value: formatUint64(d.SkippedEdges)},
		},
	}
	body, err := executeTemplateToString("diagnostics_summary", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleDiagnosticsDropsPartial(w http.ResponseWriter, r *http.Request) {
	if s.rejected == nil {
		http.Error(w, "rejected frame provider unavailable", http.StatusServiceUnavailable)
		return
	}
	data := s.rejected.Snapshot(32)
	var reasons []dropRowView
	for reason, count := range data.ByReason {
		reasons = append(reasons, dropRowView{Label: reason, Count: formatUint64(count), Sort: count})
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Sort == reasons[j].Sort {
			return reasons[i].Label < reasons[j].Label
		}
		return reasons[i].Sort > reasons[j].Sort
	})
	var dfs []dropRowView
	for df, count := range data.ByDF {
		dfs = append(dfs, dropRowView{Label: df, Count: formatUint64(count), Sort: count})
	}
	sort.Slice(dfs, func(i, j int) bool {
		if dfs[i].Sort == dfs[j].Sort {
			return dfs[i].Label < dfs[j].Label
		}
		return dfs[i].Sort > dfs[j].Sort
	})
	recent := make([]recentFrameView, 0, len(data.Recent))
	for _, item := range data.Recent {
		recent = append(recent, recentFrameView{
			Seen:        ageLabel(item.RecordedAt),
			Reason:      item.Reason,
			DF:          fmt.Sprintf("DF%d", item.DF),
			DerivedICAO: item.DerivedICAO,
			Payload:     item.Payload,
			Signal:      formatUint64(uint64(item.Signal)),
		})
	}
	payload := struct {
		Reasons []dropRowView
		DFs     []dropRowView
		Recent  []recentFrameView
	}{
		Reasons: reasons,
		DFs:     dfs,
		Recent:  recent,
	}
	body, err := executeTemplateToString("diagnostics_drops", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

// parsePinnedSet decodes a comma-separated list of ICAOs in the
// `pinned` query param. Values are upper-cased so comparison matches
// the server's %06X formatting regardless of how the browser stored
// them in localStorage.
func parsePinnedSet(r *http.Request) map[string]struct{} {
	set := map[string]struct{}{}
	raw := strings.TrimSpace(r.URL.Query().Get("pinned"))
	if raw == "" {
		return set
	}
	for _, part := range strings.Split(raw, ",") {
		p := strings.ToUpper(strings.TrimSpace(part))
		if p != "" {
			set[p] = struct{}{}
		}
	}
	return set
}

func (s *Server) handleDashboardStatePartial(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		http.Error(w, "stats provider unavailable", http.StatusServiceUnavailable)
		return
	}
	d := s.stats.Stats(true)
	if s.tracker != nil {
		d.AircraftCount = s.tracker.Count()
	}

	heroTitle := "Receiver Looks Healthy"
	heroSubtitle := fmt.Sprintf("Tracking %s aircraft with %s messages per second.",
		formatUint64(uint64(d.AircraftCount)), fmtMaybeFloat(d.MsgRate, ""))
	switch {
	case d.Overflow:
		heroTitle = "Receiver Under Pressure"
		heroSubtitle = "FIFO overflow seen recently — check RF gain and decoder saturation."
	case !d.GpsSync && d.PPSCount == 0:
		heroTitle = "Timing Needs Attention"
		heroSubtitle = "No PPS activity detected — GPS discipline is offline."
	case !d.GpsSync:
		heroTitle = "Timing Needs Attention"
		heroSubtitle = "PPS edges observed but GPS has not locked yet."
	case d.MsgRate == 0:
		heroTitle = "Waiting for Traffic"
	}

	heroMeta := []heroMetaView{
		{Label: "Traffic", Value: fmt.Sprintf("%s msg/s", fmtMaybeFloat(d.MsgRate, ""))},
		{Label: "Aircraft", Value: fmt.Sprintf("%s active", formatUint64(uint64(d.AircraftCount)))},
		{Label: "Timing", Value: ternary(d.GpsSync, "GPS disciplined", "GPS not locked")},
	}

	rfState, rfValue, rfReason := "good", "Clean", "No FIFO overflow"
	if d.Overflow {
		rfState, rfValue, rfReason = "bad", "Overflow", "FIFO overflow seen in the last 10 seconds"
	}

	decodeState, decodeValue, decodeReason := "warn", "-", "-"
	decodeRatio := 0.0
	if d.MsgRate > 0 {
		decodeRatio = d.CrcPassRate / d.MsgRate
	}
	switch {
	case d.MsgRate < MinDecodeRate:
		decodeState = "warn"
	case decodeRatio > DecodeRatioGood:
		decodeState = "good"
	case decodeRatio > DecodeRatioWarn:
		decodeState = "warn"
	default:
		decodeState = "bad"
	}
	decodeValue = fmt.Sprintf("%s valid/s", fmtMaybeFloat(d.CrcPassRate, ""))
	decodeReason = fmt.Sprintf("%s msg/s, %s drops", fmtMaybeFloat(d.MsgRate, ""), formatUint64(d.DropCount))

	timingState, timingValue, timingReason := "bad", "Uncertain", "No PPS activity"
	if d.GpsSync {
		timingState, timingValue = "good", "Locked"
		timingReason = fmt.Sprintf("%s PPS edges, %s ppm", formatUint64(uint64(d.PPSCount)), fmtMaybeFloat(d.OscillatorPPM, ""))
	} else if d.PPSCount > 0 {
		timingState = "warn"
		timingReason = fmt.Sprintf("%s PPS edges, %s ppm", formatUint64(uint64(d.PPSCount)), fmtMaybeFloat(d.OscillatorPPM, ""))
	}

	systemState, systemValue, systemReason := "good", "Stable", fmt.Sprintf("Uptime %s", uptimeLabel(d.Uptime))
	switch {
	case d.Overflow:
		systemState, systemValue, systemReason = "bad", "Overflow", "FIFO overflow seen in the last 10 seconds"
	case d.Uptime < YoungSystemUptimeS:
		systemState = "warn"
	}

	feedState, feedValue := "warn", "No clients"
	if d.ClientCount > 0 {
		feedState = "good"
		feedValue = fmt.Sprintf("%d client%s", d.ClientCount, ternary(d.ClientCount == 1, "", "s"))
	}
	feedReason := fmt.Sprintf("%s aircraft, %s ICAOs", formatUint64(uint64(d.AircraftCount)), formatUint64(uint64(d.ICAOCount)))

	pills := []healthPillView{
		{State: rfState, Label: "RF", Value: rfValue, Reason: rfReason},
		{State: decodeState, Label: "Decode", Value: decodeValue, Reason: decodeReason},
		{State: timingState, Label: "Timing", Value: timingValue, Reason: timingReason},
		{State: systemState, Label: "System", Value: systemValue, Reason: systemReason},
		{State: feedState, Label: "Feed", Value: feedValue, Reason: feedReason},
	}

	stats := []statView{
		{Label: "Messages", Value: formatUint64(d.MsgCount)},
		{Label: "Msg/s", Value: fmtMaybeFloat(d.MsgRate, "")},
		{Label: "Valid/s", Value: fmtMaybeFloat(d.CrcPassRate, "")},
		{Label: "Drops", Value: formatUint64(d.DropCount)},
		{Label: "Aircraft", Value: formatUint64(uint64(d.AircraftCount))},
		{Label: "Clients", Value: formatUint64(uint64(d.ClientCount))},
		{Label: "Uptime", Value: uptimeLabel(d.Uptime)},
		{Label: "Overflow", Value: yesNo(d.Overflow), Class: ternary(d.Overflow, "bad", "")},
	}

	var gpsStats []statView
	if d.PPSCount > 0 {
		gpsStats = []statView{
			{Label: "GPS Sync", Value: yesNo(d.GpsSync), Class: ternary(d.GpsSync, "good", "warn")},
			{Label: "PPS Edges", Value: formatUint64(uint64(d.PPSCount))},
			{Label: "Oscillator", Value: fmt.Sprintf("%s ppm", fmtMaybeFloat(d.OscillatorPPM, ""))},
			{Label: "Skipped", Value: formatUint64(d.SkippedEdges)},
		}
	}

	payload := struct {
		HeroTitle    string
		HeroSubtitle string
		HeroMeta     []heroMetaView
		Pills        []healthPillView
		Stats        []statView
		GPSStats     []statView
	}{
		HeroTitle:    heroTitle,
		HeroSubtitle: heroSubtitle,
		HeroMeta:     heroMeta,
		Pills:        pills,
		Stats:        stats,
		GPSStats:     gpsStats,
	}

	body, err := executeTemplateToString("dashboard_state", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleDashboardAircraftPartial(w http.ResponseWriter, r *http.Request) {
	pinned := parsePinnedSet(r)
	rows := make([]dashboardAircraftRow, 0)
	if s.tracker != nil {
		snap := s.tracker.Snapshot()
		refLat, refLon := s.tracker.RefPos()
		hasRef := refLat != 0 || refLon != 0
		icaos := make([]uint32, 0, len(snap))
		for icao := range snap {
			icaos = append(icaos, icao)
		}
		sort.Slice(icaos, func(i, j int) bool { return icaos[i] < icaos[j] })
		for _, icao := range icaos {
			ac := snap[icao]
			icaoHex := fmt.Sprintf("%06X", ac.ICAO)
			row := dashboardAircraftRow{
				ICAO:     icaoHex,
				Callsign: ac.Callsign,
				Squawk:   ac.Squawk,
				Signal:   strconv.FormatUint(uint64(ac.Signal), 10),
				Messages: formatUint64(ac.Messages),
				Age:      ageLabel(ac.Seen),
			}
			if ac.Altitude != 0 {
				row.Altitude = fmt.Sprintf("%dft", ac.Altitude)
			}
			if ac.Lat != 0 {
				row.Lat = fmt.Sprintf("%.4f", ac.Lat)
			}
			if ac.Lon != 0 {
				row.Lon = fmt.Sprintf("%.4f", ac.Lon)
			}
			if hasRef && ac.Lat != 0 && ac.Lon != 0 {
				d := haversineKm(refLat, refLon, ac.Lat, ac.Lon)
				row.Distance = fmt.Sprintf("%.1fkm", d)
			}
			if _, ok := pinned[icaoHex]; ok {
				row.Pinned = true
			}
			rows = append(rows, row)
		}
	}
	payload := struct {
		Count int
		Rows  []dashboardAircraftRow
	}{Count: len(rows), Rows: rows}

	body, err := executeTemplateToString("dashboard_aircraft", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleDashboardWatchlistPartial(w http.ResponseWriter, r *http.Request) {
	pinned := parsePinnedSet(r)
	// Preserve the client's original ICAO ordering (from the `pinned`
	// query string) so the cards stay put across refreshes; the set
	// map is only used for O(1) hit tests in the aircraft table.
	order := make([]string, 0, len(pinned))
	for _, part := range strings.Split(strings.TrimSpace(r.URL.Query().Get("pinned")), ",") {
		p := strings.ToUpper(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		if _, seen := pinned[p]; seen {
			order = append(order, p)
			delete(pinned, p)
		}
	}

	cards := make([]dashboardWatchCard, 0, len(order))
	if s.tracker != nil {
		snap := s.tracker.Snapshot()
		lookup := make(map[string]tracker.Aircraft, len(snap))
		for _, ac := range snap {
			lookup[fmt.Sprintf("%06X", ac.ICAO)] = ac
		}
		for _, icao := range order {
			if ac, ok := lookup[icao]; ok {
				subtitle := icao
				if ac.Squawk != "" {
					subtitle = icao + " • " + ac.Squawk
				}
				title := ac.Callsign
				if title == "" {
					title = icao
				}
				cards = append(cards, dashboardWatchCard{
					ICAO:     icao,
					Title:    title,
					Subtitle: subtitle,
					Signal:   strconv.FormatUint(uint64(ac.Signal), 10),
					Messages: formatUint64(ac.Messages),
					Age:      ageLabel(ac.Seen),
					Visible:  true,
				})
			} else {
				cards = append(cards, dashboardWatchCard{
					ICAO:     icao,
					Title:    icao,
					Subtitle: "Not currently visible",
					Visible:  false,
				})
			}
		}
	} else {
		for _, icao := range order {
			cards = append(cards, dashboardWatchCard{
				ICAO:     icao,
				Title:    icao,
				Subtitle: "Not currently visible",
				Visible:  false,
			})
		}
	}

	payload := struct {
		Count int
		Cards []dashboardWatchCard
	}{Count: len(cards), Cards: cards}

	body, err := executeTemplateToString("dashboard_watchlist", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func uptimeLabel(secs int64) string {
	if secs <= 0 {
		return "-"
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func blankOr(v string) string {
	if strings.TrimSpace(v) == "" {
		return "—"
	}
	return v
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func fmtUint32Debug(debug map[string]uint32, key string) string {
	if debug == nil {
		return "—"
	}
	v, ok := debug[key]
	if !ok {
		return "—"
	}
	return formatUint64(uint64(v))
}

func formatUint64(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func (s *Server) handleGPSDetailsPartial(w http.ResponseWriter, r *http.Request) {
	// Antenna status/power rows are intentionally omitted: the PCB-mounted
	// GNSS antenna reports spurious SHORT/OPEN states that are not useful
	// as operator warnings. Jamming and AGC detail remains below.
	var snap *gpsmon.Snapshot
	if s.gps != nil {
		snap = s.gps.Snapshot()
	}
	if snap == nil {
		snap = &gpsmon.Snapshot{}
	}
	var pps StatsData
	if s.stats != nil {
		pps = s.stats.Stats(false)
	}

	usedSats := snap.SkyDOP.USat
	if usedSats == 0 && len(snap.Sats) > 0 {
		for _, sat := range snap.Sats {
			if sat.Used {
				usedSats++
			}
		}
	}
	trackedSats := snap.SkyDOP.NSat
	if trackedSats == 0 {
		trackedSats = len(snap.Sats)
	}

	stats := []statView{
		{Label: "Receiver", Value: blankOr(snap.Receiver.Generation)},
		{Label: "Mode", Value: tmodeNameInt(snap.Receiver.TMODEMode)},
		{Label: "Fix", Value: fixModeNameInt(snap.Fix.Mode)},
		{Label: "Used Sats", Value: strconv.Itoa(usedSats)},
		{Label: "Tracked Sats", Value: strconv.Itoa(trackedSats)},
		{Label: "PPS Count", Value: formatUint64(uint64(pps.PPSCount))},
		{Label: "Time Acc", Value: nsUint32OrDash(snap.Clock.TimeAccuracyNs)},
		{Label: "Oscillator", Value: fmtMaybeFloat(pps.OscillatorPPM, " ppm")},
		{Label: "Skipped", Value: formatUint64(pps.SkippedEdges)},
		{Label: "Jamming", Value: gps.JammingStateName(snap.HW.JammingState)},
	}

	receiverRows := []kvView{
		{Key: "generation", Value: blankOr(snap.Receiver.Generation)},
		{Key: "sw", Value: blankOr(snap.Receiver.SwVersion)},
		{Key: "hw", Value: blankOr(snap.Receiver.HwVersion)},
		{Key: "time mode", Value: tmodeNameInt(snap.Receiver.TMODEMode)},
		{Key: "dyn model", Value: dynModelNameInt(snap.Receiver.NAV5.DynModel)},
	}
	jamClass := ""
	if snap.HW.JammingState == gps.JammingStateWarning {
		jamClass = "warn"
	} else if snap.HW.JammingState == gps.JammingStateCritical {
		jamClass = "bad"
	} else if snap.HW.JammingState == gps.JammingStateOK {
		jamClass = "good"
	}
	receiverRows = append(receiverRows,
		kvView{Key: "jamming", Value: gps.JammingStateName(snap.HW.JammingState), Class: jamClass},
		kvView{Key: "CW suppress", Value: fmt.Sprintf("%d / 255", snap.HW.CWSuppression)},
		kvView{Key: "AGC", Value: fmt.Sprintf("%d / 8191", snap.HW.AGCCnt)},
		kvView{Key: "noise", Value: formatUint64(uint64(snap.HW.NoisePerMS))},
	)

	locationRows := []kvView{
		{Key: "mode", Value: fixModeNameInt(snap.Fix.Mode), Class: fixModeClass(snap.Fix.Mode)},
		{Key: "time", Value: blankOr(snap.Fix.Time)},
		{Key: "lat", Value: degOrDash(snap.Fix.Lat, 7)},
		{Key: "lon", Value: degOrDash(snap.Fix.Lon, 7)},
		{Key: "alt MSL", Value: metersOrDash(snap.Fix.AltMSL)},
		{Key: "alt HAE", Value: metersOrDash(snap.Fix.AltHAE)},
		{Key: "eph", Value: metersOrDash(snap.Fix.EPH)},
		{Key: "epv", Value: metersOrDash(snap.Fix.EPV)},
		{Key: "leap", Value: dashIfZeroInt(snap.Fix.LeapSeconds, " s")},
	}
	if snap.Receiver.TMODEMode == 2 {
		locationRows = append(locationRows,
			kvView{Key: "timing mode", Value: "FIXED", Class: "pw-v-sep"},
			kvView{Key: "ECEF X", Value: cmToMetersOrDash(snap.Survey.MeanXCm)},
			kvView{Key: "ECEF Y", Value: cmToMetersOrDash(snap.Survey.MeanYCm)},
			kvView{Key: "ECEF Z", Value: cmToMetersOrDash(snap.Survey.MeanZCm)},
			kvView{Key: "surveyed acc", Value: metersFloatOrDash(snap.Survey.MeanAccMeters, 3)},
		)
	} else if snap.Receiver.TMODEMode == 1 {
		locationRows = append(locationRows,
			kvView{Key: "timing mode", Value: "SURVEY-IN", Class: "pw-v-sep"},
			kvView{Key: "active", Value: yesNo(snap.Survey.Active)},
			kvView{Key: "valid", Value: yesNo(snap.Survey.Valid)},
			kvView{Key: "duration", Value: dashIfZeroUint32(snap.Survey.DurationSec, " s")},
			kvView{Key: "obs", Value: dashIfZeroUint32(snap.Survey.Observations, "")},
			kvView{Key: "meanAcc", Value: metersFloatOrDash(snap.Survey.MeanAccMeters, 3)},
		)
	} else {
		locationRows = append(locationRows, kvView{Key: "timing mode", Value: "disabled", Class: "pw-v-sep"})
	}

	clockRows := []kvView{
		{Key: "clk bias", Value: nsInt32OrDash(snap.Clock.ClockBiasNs)},
		{Key: "clk drift", Value: nsPerSecInt32OrDash(snap.Clock.ClockDriftNsPerS)},
		{Key: "time acc", Value: nsUint32OrDash(snap.Clock.TimeAccuracyNs)},
		{Key: "freq acc", Value: psPerSecUint32OrDash(snap.Clock.FreqAccuracyPsPerS)},
		{Key: "pDOP", Value: float64OrDash(snap.DOP.PDOP, 2)},
		{Key: "tDOP", Value: float64OrDash(snap.DOP.TDOP, 2)},
		{Key: "hDOP", Value: float64OrDash(snap.DOP.HDOP, 2)},
		{Key: "vDOP", Value: float64OrDash(snap.DOP.VDOP, 2)},
	}

	ppsRows := []kvView{
		{Key: "gps sync", Value: yesNo(pps.GpsSync), Class: ternary(pps.GpsSync, "good", "bad")},
		{Key: "oscillator ppm", Value: float64OrDash(pps.OscillatorPPM, 3)},
		{Key: "carryover", Value: strconv.FormatInt(pps.Carryover, 10)},
		{Key: "avg carryover", Value: float64OrDash(pps.AvgCarryover, 2)},
		{Key: "min carryover", Value: strconv.FormatInt(pps.CarryoverMin, 10)},
		{Key: "max carryover", Value: strconv.FormatInt(pps.CarryoverMax, 10)},
		{Key: "tick interval", Value: formatUint64(pps.PpsTickRate)},
		{Key: "skipped edges", Value: formatUint64(pps.SkippedEdges), Class: ternary(pps.SkippedEdges > 0, "warn", "")},
		{Key: "PPS count", Value: formatUint64(uint64(pps.PPSCount))},
	}
	if tp5 := snap.Receiver.TP5; tp5.FreqPeriod != 0 {
		freq := fmt.Sprintf("%d µs period", tp5.FreqPeriod)
		if tp5.IsFreq {
			freq = fmt.Sprintf("%d Hz", tp5.FreqPeriod)
		}
		pulse := fmt.Sprintf("duty %d", tp5.PulseLenRatio)
		if tp5.IsLength {
			pulse = fmt.Sprintf("%d µs", tp5.PulseLenRatio)
		}
		ppsRows = append(ppsRows,
			kvView{Key: "TP5 freq", Value: freq},
			kvView{Key: "TP5 pulse", Value: pulse},
			kvView{Key: "TP5 grid", Value: ternary(tp5.GridUTC, "UTC", "GPS")},
			kvView{Key: "TP5 edge", Value: ternary(tp5.RisingAtTop, "rising@top", "falling@top")},
		)
	}

	satRows := make([]gpsSatelliteRowView, 0, len(snap.Sats))
	sats := append([]gps.Satellite(nil), snap.Sats...)
	sort.Slice(sats, func(i, j int) bool { return sats[i].SS > sats[j].SS })
	for _, sat := range sats {
		snrClass := ""
		if sat.SS < SatSNRClassBadMax {
			snrClass = "bad"
		} else if sat.SS < SatSNRClassWarnMax {
			snrClass = "warn"
		}
		snrPct := int(maxFloat(0, minFloat(100, sat.SS*100/SatSNRFullScale)))
		satRows = append(satRows, gpsSatelliteRowView{
			PRN:      dashIfZeroInt(sat.PRN, ""),
			GNSS:     gps.GNSSName(sat.GnssID),
			Az:       dashIfZeroFloat(sat.Az, 0),
			El:       dashIfZeroFloat(sat.El, 0),
			SNR:      dashIfZeroFloat(sat.SS, 0),
			SNRPct:   snrPct,
			SNRClass: snrClass,
			Used:     sat.Used,
		})
	}

	payload := struct {
		Stats            []statView
		Receiver         []kvView
		Location         []kvView
		Clock            []kvView
		PPS              []kvView
		ReceiverAge      string
		LocationAge      string
		ClockAge         string
		ReceiverErr      string
		PeriodicErr      string
		SatelliteCount   string
		SatelliteSummary string
		Satellites       []gpsSatelliteRowView
	}{
		Stats:       stats,
		Receiver:    receiverRows,
		Location:    locationRows,
		Clock:       clockRows,
		PPS:         ppsRows,
		ReceiverAge: ageLabel(snap.ReceiverAt),
		LocationAge: ageLabel(snap.FixAt),
		ClockAge:    ageLabel(snap.PeriodicAt),
		ReceiverErr: snap.ReceiverErr,
		PeriodicErr: snap.PeriodicErr,
		// Match the dashboard partial pattern: emit the count only when
		// non-zero so the section header collapses cleanly when idle.
		SatelliteCount:   ternary(trackedSats > 0, strconv.Itoa(trackedSats), ""),
		SatelliteSummary: fmt.Sprintf("%d/%d satellites used · gDOP %s · hDOP %s", usedSats, trackedSats, float64OrDash(snap.SkyDOP.GDOP, 2), float64OrDash(snap.SkyDOP.HDOP, 2)),
		Satellites:       satRows,
	}
	body, err := executeTemplateToString("gps_details", payload)
	if err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func tmodeNameInt(n int) string {
	switch n {
	case 0:
		return "disabled"
	case 1:
		return "survey-in"
	case 2:
		return "fixed"
	default:
		return fmt.Sprintf("unknown (%d)", n)
	}
}

func dynModelNameInt(n uint8) string {
	switch n {
	case 0:
		return "portable"
	case 2:
		return "stationary"
	case 3:
		return "pedestrian"
	case 4:
		return "automotive"
	case 5:
		return "sea"
	case 6:
		return "airborne<1g"
	default:
		return fmt.Sprintf("unknown (%d)", n)
	}
}

func fixModeNameInt(n int) string {
	switch n {
	case 0:
		return "none"
	case 1:
		return "no-fix"
	case 2:
		return "2D"
	case 3:
		return "3D"
	case 4:
		return "time-only"
	default:
		return fmt.Sprintf("unknown (%d)", n)
	}
}

func fixModeClass(n int) string {
	if n >= 3 {
		return "good"
	}
	if n >= 2 {
		return "warn"
	}
	return "bad"
}

func nsInt32OrDash(v int32) string       { return dashIfZeroInt64(int64(v), " ns") }
func nsPerSecInt32OrDash(v int32) string { return dashIfZeroInt64(int64(v), " ns/s") }
func nsUint32OrDash(v uint32) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%d ns", v)
}
func psPerSecUint32OrDash(v uint32) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%d ps/s", v)
}
func metersOrDash(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f m", v)
}
func metersFloatOrDash(v float64, decimals int) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.*f m", decimals, v)
}
func degOrDash(v float64, decimals int) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.*f°", decimals, v)
}
func cmToMetersOrDash(v int32) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f m", float64(v)/100)
}
func dashIfZeroInt(v int, suffix string) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%s", v, suffix)
}
func dashIfZeroInt64(v int64, suffix string) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%s", v, suffix)
}
func dashIfZeroUint(v uint, suffix string) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%s", v, suffix)
}
func dashIfZeroUint32(v uint32, suffix string) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%s", v, suffix)
}
func dashIfZeroFloat(v float64, decimals int) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.*f", decimals, v)
}
func float64OrDash(v float64, decimals int) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.*f", decimals, v)
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (s *Server) handleSetQuietScoreShift(w http.ResponseWriter, r *http.Request) {
	if s.detector == nil {
		http.Error(w, "detector config not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Value uint32 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.detector.SetQuietScoreShift(req.Value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]uint32{"quiet_score_shift": req.Value})
}

func (s *Server) handleSetSnrRatioShift(w http.ResponseWriter, r *http.Request) {
	if s.detector == nil {
		http.Error(w, "detector config not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Value uint32 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.detector.SetSnrRatioShift(req.Value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]uint32{"snr_ratio_shift": req.Value})
}

func (s *Server) handleSetHoldoff(w http.ResponseWriter, r *http.Request) {
	if s.detector == nil {
		http.Error(w, "detector config not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Value uint32 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.detector.SetHoldoff(req.Value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]uint32{"holdoff": req.Value})
}
