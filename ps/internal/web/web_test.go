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

type mockStats struct{}

func (m *mockStats) Stats(debug bool) StatsData {
	data := StatsData{
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
	if debug {
		data.Debug = map[string]uint32{
			"pre_abs_ct":    123,
			"crc_pass_ct":   45,
			"holdoff":       512,
			"deep_debug":    0,
			"msg_ct":        67,
			"invalid_df_ct": 8,
		}
	}
	return data
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
	srv := New(tr, &mockStats{}, nil, nil, nil)
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
	srv := New(tr, &mockStats{}, nil, nil, nil)
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
	srv := New(tr, &mockStats{}, nil, &mockRejected{}, nil)
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
	srv := New(tr, &mockStats{}, nil, nil, nil)
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

func TestMetricsEndpoint(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	wantContains := []string{
		"# HELP plane_watcher_msg_count_total",
		"plane_watcher_msg_count_total 100",
		"plane_watcher_aircraft_count 0",
		"plane_watcher_debug_crc_pass_ct 45",
		"plane_watcher_ratio_crc_pass_per_som 0",
		"plane_watcher_ratio_pre_det_per_pre_abs 0",
	}
	for _, want := range wantContains {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q\nbody:\n%s", want, body)
		}
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
}

func TestSetQuietScoreShift(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	md := &mockDetector{}
	srv := New(tr, &mockStats{}, md, nil, nil)

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
	srv := New(tr, &mockStats{}, md, nil, nil)

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
	srv := New(tr, &mockStats{}, md, nil, nil)

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
	srv := New(tr, &mockStats{}, nil, nil, nil)

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
	srv := New(tr, &mockStats{}, nil, nil, nil)
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
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	labels := []string{"Traffic Trends", "RF Pressure", "Receiver", "Diagnostics",
		"dashboard-state", "dashboard-aircraft", "dashboard-watchlist"}
	for _, label := range labels {
		if !strings.Contains(body, label) {
			t.Errorf("dashboard missing label %q", label)
		}
	}

	notExpected := []string{"GPS Timing Detail", "Rejected Frames", "derived_icao"}
	for _, label := range notExpected {
		if strings.Contains(body, label) {
			t.Errorf("dashboard unexpectedly still contains %q", label)
		}
	}
}

func TestDashboardStatePartialServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/dashboard/state", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Receiver Status", "pw-hero", "pw-status-pill", "Messages", "GPS Sync"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard state partial missing %q", want)
		}
	}
}

func TestDashboardAircraftPartialServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/dashboard/aircraft", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"<h2>Aircraft", "ICAO", "Callsign", "Beacon", "No aircraft"} {
		if !strings.Contains(body, want) {
			t.Errorf("aircraft partial missing %q", want)
		}
	}
}

func TestDashboardWatchlistPartialServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/dashboard/watchlist", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"<h2>Watchlist", "No beacons pinned yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("watchlist partial missing %q", want)
		}
	}
}

func TestDashboardWatchlistPartialRendersPinned(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/dashboard/watchlist?pinned=ABC123,DEADBE", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"ABC123", "DEADBE", "Not currently visible"} {
		if !strings.Contains(body, want) {
			t.Errorf("watchlist partial missing %q", want)
		}
	}
	if strings.Contains(body, "No beacons pinned yet") {
		t.Error("watchlist unexpectedly shows empty-state card when pinned entries provided")
	}
}

func TestDiagnosticsPageServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/diagnostics", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	labels := []string{"Engineering View", "diagnostics-summary-fragment", "diagnostics-drops-fragment", "Receiver"}
	for _, label := range labels {
		if !strings.Contains(body, label) {
			t.Errorf("diagnostics missing %q", label)
		}
	}
}

func TestDiagnosticsSummaryPartialServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/diagnostics/summary", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, label := range []string{"Raw Counters", "Timing Detail", "pre_abs_ct", "gps sync"} {
		if !strings.Contains(body, label) {
			t.Errorf("summary partial missing %q", label)
		}
	}
}

func TestDiagnosticsDropsPartialServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, &mockRejected{}, nil)
	req := httptest.NewRequest("GET", "/partials/diagnostics/drops", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, label := range []string{"Drop Reasons", "Recent Frames", "icao_filter_miss", "ABC123"} {
		if !strings.Contains(body, label) {
			t.Errorf("drops partial missing %q", label)
		}
	}
}

func TestReceiverPageServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/receiver", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	labels := []string{"Detector", "Diagnostics"}
	for _, label := range labels {
		if !strings.Contains(body, label) {
			t.Errorf("receiver page missing %q", label)
		}
	}
}

func TestReceiverStatePartialServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/receiver/state", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, label := range []string{"Detector State", "quiet score shift"} {
		if !strings.Contains(body, label) {
			t.Errorf("receiver partial missing %q", label)
		}
	}
}

func TestHandleGNSS_WithoutProvider(t *testing.T) {
	tr := tracker.New(0, 0)
	srv := New(tr, &mockStats{}, nil, nil, nil)
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
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/gps", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"plane-watcher — GPS", "health-fix", "health-pps", "/api/gps"} {
		if !strings.Contains(body, want) {
			t.Errorf("gps.html missing %q", want)
		}
	}
}

func TestGPSDetailsPartialWithoutProvider(t *testing.T) {
	tr := tracker.New(0, 0)
	srv := New(tr, &mockStats{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/partials/gps/details", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Receiver", "Clock and Timing", "PPS Detail", "Satellites"} {
		if !strings.Contains(body, want) {
			t.Errorf("gps partial missing %q", want)
		}
	}
}
