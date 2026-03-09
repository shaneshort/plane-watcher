package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/diag"
	"github.com/plane-watcher/plane-feeder/internal/radio"
	"github.com/plane-watcher/plane-feeder/internal/tracker"
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
	Radio         radio.Status      `json:"radio"`
	Debug         map[string]uint32 `json:"debug,omitempty"`
}

type StatsProvider interface {
	Stats(debug bool) StatsData
}

type RadioController interface {
	SetGainMode(mode string) error
	SetGain(gainDB string) error
}

type DetectorConfig interface {
	SetQuietScoreShift(val uint32) error
	SetSnrRatioShift(val uint32) error
	SetHoldoff(val uint32) error
}

type RejectedFrameProvider interface {
	Snapshot(limit int) diag.RejectedFrameSummary
}

type Server struct {
	tracker  *tracker.Tracker
	stats    StatsProvider
	radio    RadioController
	detector DetectorConfig
	rejected RejectedFrameProvider
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

func New(t *tracker.Tracker, sp StatsProvider, rc RadioController, dc DetectorConfig, rp RejectedFrameProvider) *Server {
	return &Server{tracker: t, stats: sp, radio: rc, detector: dc, rejected: rp}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/aircraft", s.handleAircraft)
	mux.HandleFunc("GET /api/drops", s.handleDrops)
	mux.HandleFunc("POST /api/radio/gain-mode", s.handleSetGainMode)
	mux.HandleFunc("POST /api/radio/gain", s.handleSetGain)
	mux.HandleFunc("POST /api/detector/quiet-score-shift", s.handleSetQuietScoreShift)
	mux.HandleFunc("POST /api/detector/snr-ratio-shift", s.handleSetSnrRatioShift)
	mux.HandleFunc("POST /api/detector/holdoff", s.handleSetHoldoff)
	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServerFS(staticSub))
	return mux
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

func (s *Server) handleSetGainMode(w http.ResponseWriter, r *http.Request) {
	if s.radio == nil {
		http.Error(w, "radio not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		http.Error(w, `missing "mode" field`, http.StatusBadRequest)
		return
	}
	if err := s.radio.SetGainMode(req.Mode); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"gain_mode": req.Mode})
}

func (s *Server) handleSetGain(w http.ResponseWriter, r *http.Request) {
	if s.radio == nil {
		http.Error(w, "radio not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		GainDB string `json:"gain_db"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.GainDB == "" {
		http.Error(w, `missing "gain_db" field`, http.StatusBadRequest)
		return
	}
	if err := s.radio.SetGain(req.GainDB); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"gain_mode": "manual", "gain_db": req.GainDB})
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
