package main

// Tests for the human-readable and JSON renderers. The visual tests use
// committed golden files under testdata/; run `UPDATE_GOLDEN=1 go test
// ./cmd/ubx/` to re-baseline after intentional format changes.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/plane-watcher/plane-feeder/internal/gps"
)

// plainTerm returns a term whose styles render without ANSI codes,
// so golden files stay stable regardless of the host TTY profile.
// Constructed via a renderer pointed at io.Discard which auto-detects
// as no-colour.
func plainTerm() term {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii) // strip all styling for stable goldens
	return term{
		renderer:    r,
		boldStyle:   r.NewStyle().Bold(true),
		dimStyle:    r.NewStyle().Faint(true),
		greenStyle:  r.NewStyle().Foreground(lipgloss.Color("2")),
		yellowStyle: r.NewStyle().Foreground(lipgloss.Color("3")),
		redStyle:    r.NewStyle().Foreground(lipgloss.Color("1")),
	}
}

// goldenAssert compares got against testdata/<TestName>.golden. Set
// UPDATE_GOLDEN=1 to rewrite the golden file from the current output.
// On mismatch, writes the actual output beside the golden so the
// failure includes a diff-friendly artefact.
func goldenAssert(t *testing.T, got string) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	p := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden %s (run with UPDATE_GOLDEN=1 to create): %v", p, err)
	}
	if got != string(want) {
		_ = os.WriteFile(p+".actual", []byte(got), 0o644)
		t.Errorf("output differs from %s\n(actual written to %s.actual)\n--- got ---\n%s\n--- want ---\n%s",
			p, p, got, string(want))
	}
}

// ----- pure-function tests -----

func TestThousands(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1_000, "1,000"},
		{12_345, "12,345"},
		{1_234_567, "1,234,567"},
		{-1, "-1"},
		{-999, "-999"},
		{-12_345, "-12,345"},
		{-237_026_858, "-237,026,858"},
	}
	for _, c := range cases {
		if got := thousands(c.in); got != c.want {
			t.Errorf("thousands(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatNsValue(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 ns"},
		{42, "42 ns"},
		{999, "999 ns"},
		{1_000, "1.000 µs"},
		{12_345, "12.345 µs"},
		{999_999, "999.999 µs"},
		{1_000_000, "1.000 ms"},
		{-3_422, "-3.422 µs"},
		{-12, "-12 ns"},
		{1_500_000_000, "1.500 s"},
	}
	for _, c := range cases {
		if got := formatNsValue(c.in); got != c.want {
			t.Errorf("formatNsValue(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatMetres(t *testing.T) {
	cases := []struct {
		cm   int32
		want string
	}{
		{0, "0.00 m"},
		{100, "1.00 m"},
		{-237_026_858, "-2,370,268.58 m"},
		{487_138_237, "4,871,382.37 m"},
		{-100, "-1.00 m"},
		{-1, "-0.01 m"},
	}
	for _, c := range cases {
		if got := formatMetres(c.cm); got != c.want {
			t.Errorf("formatMetres(%d) = %q, want %q", c.cm, got, c.want)
		}
	}
}

func TestFormatCm(t *testing.T) {
	if got, want := formatCm(-237_026_858), "(-237,026,858 cm)"; got != want {
		t.Errorf("formatCm = %q, want %q", got, want)
	}
}

func TestFormatCmHP(t *testing.T) {
	if got, want := formatCmHP(123_456, 7), "(123,456 cm + +7 × 0.1 mm)"; got != want {
		t.Errorf("formatCmHP positive = %q, want %q", got, want)
	}
	if got, want := formatCmHP(-123_456, -7), "(-123,456 cm + -7 × 0.1 mm)"; got != want {
		t.Errorf("formatCmHP negative = %q, want %q", got, want)
	}
}

func TestSurveyStatus(t *testing.T) {
	const minDur uint32 = 300
	const targetAcc = 2.0
	cases := []struct {
		name string
		st   gps.SVINStatus
		want string
	}{
		{"surveying mid", gps.SVINStatus{DurationSec: 100, MeanAccMeters: 3.0, Active: true}, "surveying"},
		{"waiting on accuracy", gps.SVINStatus{DurationSec: 350, MeanAccMeters: 3.0, Active: true}, "waiting"},
		{"converged", gps.SVINStatus{DurationSec: 350, MeanAccMeters: 1.5, Valid: true, Active: false}, "converged"},
		{"under target but still active", gps.SVINStatus{DurationSec: 100, MeanAccMeters: 1.5, Active: true}, "surveying"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := surveyStatus(c.st, minDur, targetAcc); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestProgressBar(t *testing.T) {
	// Empty / full / mid / past-deadline cases. Verifies cap-at-100%.
	cases := []struct {
		name           string
		dur, min       uint32
		width          int
		wantStartsWith string
		wantEndsWith   string
	}{
		{"zero", 0, 300, 10, "▕", "░░░░░░░░░░▏"},
		{"full", 300, 300, 10, "▕██████████", "▏"},
		{"capped over", 500, 300, 10, "▕██████████", "▏"},
		{"mid", 150, 300, 10, "▕█████░░░░░", "▏"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := progressBar(c.dur, c.min, c.width)
			if !strings.HasPrefix(got, c.wantStartsWith) {
				t.Errorf("prefix: got %q, want prefix %q", got, c.wantStartsWith)
			}
			if !strings.HasSuffix(got, c.wantEndsWith) {
				t.Errorf("suffix: got %q, want suffix %q", got, c.wantEndsWith)
			}
		})
	}
}

// ----- visual golden tests -----

func TestPrintTMODEHuman_Fixed(t *testing.T) {
	raw, _ := hex.DecodeString("02000000d641dff1bd23091dffa900ec220400002c010000d0070000")
	var buf bytes.Buffer
	if err := printTMODEHuman(&buf, plainTerm(), gps.GenM8, raw); err != nil {
		t.Fatal(err)
	}
	goldenAssert(t, buf.String())
}

func TestPrintTMODEHuman_SurveyIn(t *testing.T) {
	raw, _ := hex.DecodeString("010000004441dff18525091d50a900ec8d0400002c010000d0070000")
	var buf bytes.Buffer
	if err := printTMODEHuman(&buf, plainTerm(), gps.GenM8, raw); err != nil {
		t.Fatal(err)
	}
	goldenAssert(t, buf.String())
}

func TestPrintTMODEHuman_Disabled(t *testing.T) {
	raw, _ := hex.DecodeString("00000000000000000000000000000000000000000000000000000000")
	var buf bytes.Buffer
	if err := printTMODEHuman(&buf, plainTerm(), gps.GenM8, raw); err != nil {
		t.Fatal(err)
	}
	goldenAssert(t, buf.String())
}

func TestPrintStatusHuman_FullFixedMode(t *testing.T) {
	tmodeRaw, _ := hex.DecodeString("02000000d641dff1bd23091dffa900ec220400002c010000d0070000")
	rpt := gps.StatusReport{
		Generation: gps.GenM8,
		MonVer: gps.MonVer{
			SwVersion:  "EXT CORE 3.01 (111141)",
			HwVersion:  "00080000",
			Extensions: []string{"ROM BASE 2.01 (75331)", "FWVER=TIM 1.10", "GPS;GLO;GAL;BDS"},
		},
		TMODE:     gps.TMODEPayload{Generation: gps.GenM8, Raw: tmodeRaw},
		TMODEMode: 2,
		NAV5:      gps.NAV5Summary{DynModel: 2, FixMode: 3, MinElev: 5, UtcStandard: 3},
		TP5: gps.TP5Summary{
			Active: true, LockGpsFreq: true, FreqPeriod: 1, PulseLenRatio: 500_000,
			IsFreq: true, IsLength: true, AlignToTow: true, RisingAtTop: true, GridUTC: true,
		},
		Clock: gps.ClockStatus{
			TimeAccuracyNs: 8, FreqAccuracyPsPerS: 12_500,
			ClockBiasNs: -3_422, ClockDriftNsPerS: -12,
		},
		SKY: &gps.SKY{
			HDOP: 0.56, VDOP: 0.84, PDOP: 1.01, NSat: 23, USat: 20,
			Satellites: []gps.Satellite{
				{PRN: 85, GnssID: 6, Az: 31, El: 56, SS: 50, Used: true},
				{PRN: 24, GnssID: 0, Az: 20, El: 71, SS: 47, Used: true},
				{PRN: 13, GnssID: 0, Az: 194, El: 59, SS: 46, Used: false},
			},
		},
	}
	var buf bytes.Buffer
	printStatusHuman(&buf, plainTerm(), rpt)
	goldenAssert(t, buf.String())
}

func TestPrintStatusHuman_SurveyInMode(t *testing.T) {
	tmodeRaw, _ := hex.DecodeString("010000004441dff18525091d50a900ec8d0400002c010000d0070000")
	rpt := gps.StatusReport{
		Generation: gps.GenM8,
		MonVer:     gps.MonVer{SwVersion: "EXT CORE 3.01 (111141)", HwVersion: "00080000"},
		TMODE:      gps.TMODEPayload{Generation: gps.GenM8, Raw: tmodeRaw},
		TMODEMode:  1,
		SVIN: gps.SVINStatus{
			DurationSec: 120, Observations: 121, MeanAccMeters: 1.35,
			Valid: false, Active: true,
			MeanXCm: -237_027_004, MeanYCm: 487_138_693, MeanZCm: -335_500_976,
		},
		NAV5:  gps.NAV5Summary{DynModel: 2, FixMode: 3, MinElev: 5, UtcStandard: 3},
		TP5:   gps.TP5Summary{Active: true, FreqPeriod: 1, IsFreq: true},
		Clock: gps.ClockStatus{TimeAccuracyNs: 24},
		SKY:   &gps.SKY{HDOP: 0.71, VDOP: 1.0, PDOP: 1.2, NSat: 18, USat: 14},
	}
	var buf bytes.Buffer
	printStatusHuman(&buf, plainTerm(), rpt)
	goldenAssert(t, buf.String())
}

func TestSurveyPanelLines_Surveying(t *testing.T) {
	st := gps.SVINStatus{DurationSec: 92, Observations: 93, MeanAccMeters: 1.29, Active: true}
	got := strings.Join(surveyPanelLines(plainTerm(), st, 300, 2.0, "surveying"), "\n")
	goldenAssert(t, got)
}

func TestSurveyPanelLines_Waiting(t *testing.T) {
	st := gps.SVINStatus{DurationSec: 301, Observations: 302, MeanAccMeters: 2.53, Active: true}
	got := strings.Join(surveyPanelLines(plainTerm(), st, 300, 2.0, "waiting"), "\n")
	goldenAssert(t, got)
}

func TestSurveyPanelLines_Converged(t *testing.T) {
	st := gps.SVINStatus{DurationSec: 302, Observations: 301, MeanAccMeters: 1.082, Valid: true}
	got := strings.Join(surveyPanelLines(plainTerm(), st, 300, 2.0, "converged"), "\n")
	goldenAssert(t, got)
}

func TestBoxTable_HeadersAndAlignment(t *testing.T) {
	var buf bytes.Buffer
	boxTable(&buf, plainTerm(),
		[]string{"PRN", "GNSS", "Az", "El", "SNR", "Used"},
		[]bool{alignRight, alignLeft, alignRight, alignRight, alignRight, alignLeft},
		[][]string{
			{"85", "GLONASS", "31°", "56°", "50", "✓"},
			{"24", "GPS", "20°", "71°", "47", "✓"},
			{"201", "BeiDou", "90°", "50°", "31", "·"},
		})
	goldenAssert(t, buf.String())
}

// ----- JSON shape tests -----

func TestBuildTMODEJSON_FixedM8(t *testing.T) {
	raw, _ := hex.DecodeString("02000000d641dff1bd23091dffa900ec220400002c010000d0070000")
	out := buildTMODEJSON(gps.GenM8, raw)
	if out["time_mode_name"] != "fixed" {
		t.Errorf("time_mode_name = %v, want fixed", out["time_mode_name"])
	}
	if out["tmode_msg"] != "CFG-TMODE2" {
		t.Errorf("tmode_msg = %v, want CFG-TMODE2", out["tmode_msg"])
	}
	fp, ok := out["fixed_position"].(map[string]any)
	if !ok {
		t.Fatalf("fixed_position not a map: %T", out["fixed_position"])
	}
	if fp["active"] != true {
		t.Errorf("fixed_position.active = %v, want true", fp["active"])
	}
	if fp["ecef_x_cm"] != int32(-237_026_858) {
		t.Errorf("ecef_x_cm = %v, want -237026858", fp["ecef_x_cm"])
	}
	if fp["accuracy_mm"] != uint32(1_058) {
		t.Errorf("accuracy_mm = %v, want 1058", fp["accuracy_mm"])
	}
	si, ok := out["survey_in_thresholds"].(map[string]any)
	if !ok {
		t.Fatalf("survey_in_thresholds not a map")
	}
	if si["active"] != false {
		t.Errorf("survey_in_thresholds.active = %v, want false", si["active"])
	}
	if si["min_duration_s"] != uint32(300) {
		t.Errorf("min_duration_s = %v, want 300", si["min_duration_s"])
	}

	// Round-trip through encoding/json to verify it marshals without error.
	if _, err := json.Marshal(out); err != nil {
		t.Errorf("json marshal: %v", err)
	}
}

func TestBuildStatusJSON_Shape(t *testing.T) {
	tmodeRaw, _ := hex.DecodeString("02000000d641dff1bd23091dffa900ec220400002c010000d0070000")
	rpt := gps.StatusReport{
		Generation: gps.GenM8,
		MonVer:     gps.MonVer{SwVersion: "SW", HwVersion: "HW", Extensions: []string{"X"}},
		TMODE:      gps.TMODEPayload{Generation: gps.GenM8, Raw: tmodeRaw},
		TMODEMode:  2,
		NAV5:       gps.NAV5Summary{DynModel: 2, FixMode: 3, MinElev: 5, UtcStandard: 3},
		TP5:        gps.TP5Summary{Active: true, FreqPeriod: 1, IsFreq: true},
		Clock:      gps.ClockStatus{TimeAccuracyNs: 8, FreqAccuracyPsPerS: 12_500, ClockBiasNs: -3422, ClockDriftNsPerS: -12, ITOW: 234_567_000},
		SKY: &gps.SKY{
			HDOP: 0.5, NSat: 10, USat: 8,
			Satellites: []gps.Satellite{{PRN: 1, GnssID: 0, Az: 10, El: 20, SS: 30, Used: true}},
		},
	}
	out := buildStatusJSON(rpt)
	for _, key := range []string{"generation", "receiver", "time_mode", "nav5", "tp5", "timing", "satellites"} {
		if _, ok := out[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}
	timing, ok := out["timing"].(map[string]any)
	if !ok {
		t.Fatalf("timing not a map")
	}
	if timing["time_accuracy_ns"] != uint32(8) {
		t.Errorf("time_accuracy_ns = %v, want 8", timing["time_accuracy_ns"])
	}
	if timing["clock_bias_ns"] != int32(-3422) {
		t.Errorf("clock_bias_ns = %v, want -3422", timing["clock_bias_ns"])
	}
	sats, ok := out["satellites"].(map[string]any)
	if !ok {
		t.Fatalf("satellites not a map")
	}
	if sats["seen"] != 10 {
		t.Errorf("satellites.seen = %v, want 10", sats["seen"])
	}
	list, ok := sats["satellites"].([]map[string]any)
	if !ok {
		t.Fatalf("satellites.satellites not []map[string]any: %T", sats["satellites"])
	}
	if len(list) != 1 || list[0]["prn"] != 1 || list[0]["gnss_name"] != "GPS" {
		t.Errorf("satellite list shape unexpected: %+v", list)
	}

	if _, err := json.Marshal(out); err != nil {
		t.Errorf("json marshal: %v", err)
	}
}
