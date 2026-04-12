package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/diag"
	"github.com/plane-watcher/plane-feeder/internal/tracker"
)

type mockDetector struct {
	quietScoreShift uint32
	snrRatioShift   uint32
	holdoff         uint32
}

func (m *mockDetector) SetQuietScoreShift(val uint32) error {
	if val > 7 {
		return fmt.Errorf("out of range")
	}
	m.quietScoreShift = val
	return nil
}

func (m *mockDetector) SetSnrRatioShift(val uint32) error {
	if val > 7 {
		return fmt.Errorf("out of range")
	}
	m.snrRatioShift = val
	return nil
}

func (m *mockDetector) SetHoldoff(val uint32) error {
	if val > 4095 {
		return fmt.Errorf("out of range")
	}
	m.holdoff = val
	return nil
}

type mockRadio struct {
	mode   string
	gainDB string
}

func (m *mockRadio) SetGainMode(mode string) error {
	valid := map[string]bool{"manual": true, "slow_attack": true, "fast_attack": true, "hybrid": true}
	if !valid[mode] {
		return fmt.Errorf("invalid gain mode %q", mode)
	}
	m.mode = mode
	return nil
}

func (m *mockRadio) SetGain(gainDB string) error {
	m.mode = "manual"
	m.gainDB = gainDB
	return nil
}

type mockStats struct{}

func (m *mockStats) Stats(debug bool) StatsData {
	return StatsData{
		Uptime:        60,
		MsgCount:      100,
		MsgRate:       10.5,
		CrcPassRate:   8.2,
		DropCount:     5,
		ICAOCount:     10,
		ClientCount:   2,
		PPSCount:      42,
		GpsSync:       true,
		OscillatorPPM: -14.703,
		Carryover:     -1470,
		AvgCarryover:  -1470.0,
		CarryoverMin:  -1500,
		CarryoverMax:  -1400,
		PpsTickRate:   99_998_530,
		SkippedEdges:  0,
	}
}

type mockRejected struct{}

func (m *mockRejected) Snapshot(limit int) diag.RejectedFrameSummary {
	return diag.RejectedFrameSummary{
		Total: 2,
		ByDF: map[string]uint64{
			"DF4":  1,
			"DF20": 1,
		},
		Recent: []diag.RejectedFrame{
			{
				Sequence:    2,
				RecordedAt:  time.Unix(1_700_000_000, 0).UTC(),
				Reason:      diag.ReasonICAOFilterMiss,
				DF:          20,
				Length:      14,
				Payload:     "A000000000000000000000000000",
				DerivedICAO: "ABC123",
				Signal:      255,
				RPL:         0x00ABCDEF,
				TOA:         123456,
			},
		},
	}
}

func TestStatsEndpoint(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var data StatsData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data.MsgCount != 100 {
		t.Errorf("MsgCount = %d, want 100", data.MsgCount)
	}
	if data.MsgRate != 10.5 {
		t.Errorf("MsgRate = %f, want 10.5", data.MsgRate)
	}
	if data.CrcPassRate != 8.2 {
		t.Errorf("CrcPassRate = %f, want 8.2", data.CrcPassRate)
	}
}

func TestAircraftEndpoint(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/api/aircraft", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var aircraft []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&aircraft); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(aircraft) != 0 {
		t.Errorf("got %d aircraft, want 0", len(aircraft))
	}
}

func TestDropsEndpoint(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, &mockRejected{}, nil)
	req := httptest.NewRequest("GET", "/api/drops", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var data diag.RejectedFrameSummary
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data.Total != 2 {
		t.Fatalf("total = %d, want 2", data.Total)
	}
	if got := data.ByDF["DF20"]; got != 1 {
		t.Fatalf("DF20 = %d, want 1", got)
	}
	if len(data.Recent) != 1 {
		t.Fatalf("recent len = %d, want 1", len(data.Recent))
	}
	if data.Recent[0].DerivedICAO != "ABC123" {
		t.Fatalf("derived_icao = %q, want ABC123", data.Recent[0].DerivedICAO)
	}
}

func TestDashboardServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Error("no Content-Type header")
	}
}

func TestSetGainMode(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	mr := &mockRadio{}
	srv := New(tr, &mockStats{}, mr, nil, nil, nil)

	body := strings.NewReader(`{"mode":"slow_attack"}`)
	req := httptest.NewRequest("POST", "/api/radio/gain-mode", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if mr.mode != "slow_attack" {
		t.Errorf("mode = %q, want slow_attack", mr.mode)
	}
}

func TestSetGainModeInvalid(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	mr := &mockRadio{}
	srv := New(tr, &mockStats{}, mr, nil, nil, nil)

	body := strings.NewReader(`{"mode":"turbo"}`)
	req := httptest.NewRequest("POST", "/api/radio/gain-mode", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSetGain(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	mr := &mockRadio{}
	srv := New(tr, &mockStats{}, mr, nil, nil, nil)

	body := strings.NewReader(`{"gain_db":"40"}`)
	req := httptest.NewRequest("POST", "/api/radio/gain", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if mr.mode != "manual" {
		t.Errorf("mode = %q, want manual", mr.mode)
	}
	if mr.gainDB != "40" {
		t.Errorf("gainDB = %q, want 40", mr.gainDB)
	}
}

func TestSetGainNoRadio(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)

	body := strings.NewReader(`{"gain_db":"40"}`)
	req := httptest.NewRequest("POST", "/api/radio/gain", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestSetQuietScoreShift(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	md := &mockDetector{}
	srv := New(tr, &mockStats{}, nil, md, nil, nil)

	body := strings.NewReader(`{"value":2}`)
	req := httptest.NewRequest("POST", "/api/detector/quiet-score-shift", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if md.quietScoreShift != 2 {
		t.Errorf("quietScoreShift = %d, want 2", md.quietScoreShift)
	}
}

func TestSetSnrRatioShift(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	md := &mockDetector{}
	srv := New(tr, &mockStats{}, nil, md, nil, nil)

	body := strings.NewReader(`{"value":3}`)
	req := httptest.NewRequest("POST", "/api/detector/snr-ratio-shift", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if md.snrRatioShift != 3 {
		t.Errorf("snrRatioShift = %d, want 3", md.snrRatioShift)
	}
}

func TestSetQuietScoreShiftOutOfRange(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	md := &mockDetector{}
	srv := New(tr, &mockStats{}, nil, md, nil, nil)

	body := strings.NewReader(`{"value":8}`)
	req := httptest.NewRequest("POST", "/api/detector/quiet-score-shift", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSetDetectorNoConfig(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)

	body := strings.NewReader(`{"value":1}`)
	req := httptest.NewRequest("POST", "/api/detector/quiet-score-shift", body)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestStatsEndpointIncludesGPSFields(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var data map[string]any
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fields := []string{"gps_sync", "oscillator_ppm", "carryover", "avg_carryover",
		"carryover_min", "carryover_max", "pps_interval_ticks", "skipped_edges"}
	for _, f := range fields {
		if _, ok := data[f]; !ok {
			t.Errorf("missing field %q in /api/stats response", f)
		}
	}
	if data["gps_sync"] != true {
		t.Errorf("gps_sync: got %v, want true", data["gps_sync"])
	}
	if v, ok := data["pps_interval_ticks"].(float64); !ok || uint64(v) != 99_998_530 {
		t.Errorf("pps_interval_ticks: got %v, want 99998530", data["pps_interval_ticks"])
	}
}

func TestDashboardContainsGPSLabels(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// Main GPS stats row labels.
	labels := []string{"GPS Sync", "Oscillator", "PPS Edges", "Skipped"}
	for _, label := range labels {
		if !strings.Contains(body, label) {
			t.Errorf("dashboard missing label %q", label)
		}
	}
	// Advanced section: GPS timing detail and rejected frames.
	advancedLabels := []string{"GPS Timing Detail", "Rejected Frames", "derived_icao"}
	for _, label := range advancedLabels {
		if !strings.Contains(body, label) {
			t.Errorf("dashboard advanced section missing %q", label)
		}
	}
}

func TestHandleGNSS_WithoutProvider(t *testing.T) {
	tr := tracker.New(0, 0)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/api/gps", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["gps"] != nil {
		t.Errorf("expected gps=null when no provider, got %v", resp["gps"])
	}
	if _, ok := resp["gps_error"]; !ok {
		t.Error("expected gps_error field when no provider")
	}
	if _, ok := resp["pps"]; !ok {
		t.Error("expected pps field populated from StatsProvider")
	}
}

func TestHandleGNSS_ServesStaticPage(t *testing.T) {
	tr := tracker.New(0, 0)
	srv := New(tr, &mockStats{}, nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/gps", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// Sanity-check the HTML contains things we expect.
	for _, want := range []string{"plane-watcher — GPS", "hide-antenna", "/api/gps"} {
		if !strings.Contains(body, want) {
			t.Errorf("gps.html missing %q", want)
		}
	}
}
