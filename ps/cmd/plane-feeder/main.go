package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/beast"
	"github.com/plane-watcher/plane-feeder/internal/chrony"
	"github.com/plane-watcher/plane-feeder/internal/crc"
	"github.com/plane-watcher/plane-feeder/internal/diag"
	"github.com/plane-watcher/plane-feeder/internal/icao"
	"github.com/plane-watcher/plane-feeder/internal/pps"
	"github.com/plane-watcher/plane-feeder/internal/radio"
	"github.com/plane-watcher/plane-feeder/internal/regs"
	"github.com/plane-watcher/plane-feeder/internal/reorder"
	"github.com/plane-watcher/plane-feeder/internal/server"
	"github.com/plane-watcher/plane-feeder/internal/tracker"
	"github.com/plane-watcher/plane-feeder/internal/web"
)

type statsSource struct {
	startTime   time.Time
	reader      regs.RegisterReader
	deepDebug   bool
	filter      *icao.Filter
	beastSrv    *server.Server
	rd          *radio.Radio
	ppsWatcher  *pps.Watcher
	msgCount    *atomic.Uint64
	dropCount   *atomic.Uint64
	msgRate     *atomic.Int64 // msgs/sec * 10 (fixed-point, written by poll loop)
	crcPassRate *atomic.Int64 // CRC-valid msgs/sec * 10 (fixed-point)
}

func (s *statsSource) Stats(debug bool) web.StatsData {
	status := s.reader.Read32(regs.RegStatus)
	ppsReg := regs.ReadPps(s.reader)

	d := web.StatsData{
		Uptime:      int64(time.Since(s.startTime).Seconds()),
		MsgCount:    s.msgCount.Load(),
		MsgRate:     float64(s.msgRate.Load()) / 10.0,
		CrcPassRate: float64(s.crcPassRate.Load()) / 10.0,
		DropCount:   s.dropCount.Load(),
		ICAOCount:   s.filter.Count(),
		ClientCount: s.beastSrv.ClientCount(),
		PPSCount:    ppsReg.Count,
		Overflow:    status&regs.StatusOverflow != 0,
	}

	if s.ppsWatcher != nil {
		ps := s.ppsWatcher.Stats()
		d.GpsSync = ps.GpsSync
		d.OscillatorPPM = ps.OscillatorPPM
		d.Carryover = ps.Carryover
		d.AvgCarryover = ps.AvgCarryover
		d.CarryoverMin = ps.CarryoverMin
		d.CarryoverMax = ps.CarryoverMax
		d.PpsTickRate = ps.MeasuredTicks
		d.SkippedEdges = ps.SkippedEdges
	}

	if s.rd != nil {
		d.Radio = s.rd.ReadStatus()
	}

	if debug {
		d.Debug = readDebugCounters(s.reader, s.deepDebug)
	}

	return d
}

func readDebugCounters(r regs.RegisterReader, deepDebug bool) map[string]uint32 {
	cfg := r.Read32(regs.RegConfig)
	debugCounters := map[string]uint32{
		"deep_debug":         0,
		"quiet_score_shift":  cfg & regs.ConfigQuietScoreShiftMask,
		"snr_ratio_shift":    (cfg & regs.ConfigSnrRatioShiftMask) >> 3,
		"holdoff":            (cfg & regs.ConfigHoldoffMask) >> regs.ConfigHoldoffShift,
		"raw_power_max":      regs.ReadDbg(r, regs.DbgRawPowerMax),
		"raw_power_thr_ct":   regs.ReadDbg(r, regs.DbgRawPowerThrCt),
		"raw_iq_75pct_ct":    regs.ReadDbg(r, regs.DbgRawIq75PctCt),
		"raw_iq_87p5pct_ct":  regs.ReadDbg(r, regs.DbgRawIq87P5PctCt),
		"raw_iq_nearrail_ct": regs.ReadDbg(r, regs.DbgRawIqNearrailCt),
		"raw_power_sat_ct":   regs.ReadDbg(r, regs.DbgRawPowerSatCt),
		"power_max":          regs.ReadDbg(r, regs.DbgPowerMax),
		"edge_thr_ct":        regs.ReadDbg(r, regs.DbgEdgeThrCt),
		"power_thr_ct":       regs.ReadDbg(r, regs.DbgPowerThrCt),
		"edge_ct":            regs.ReadDbg(r, regs.DbgEdgeCt),
		"pre_abs_ct":         regs.ReadDbg(r, regs.DbgPreAbsCt),
		"pre_quiet_ct":       regs.ReadDbg(r, regs.DbgPreQuietCt),
		"pre_snr_ct":         regs.ReadDbg(r, regs.DbgPreSnrCt),
		"pre_pass_ct":        regs.ReadDbg(r, regs.DbgPrePassCt),
		"pre_det_ct":         regs.ReadDbg(r, regs.DbgPreDetCt),
		"som_ct":             regs.ReadDbg(r, regs.DbgSomCt),
		"invalid_df_ct":      regs.ReadDbg(r, regs.DbgInvalidDfCt),
		"crc_pass_ct":        regs.ReadDbg(r, regs.DbgCrcPassCt),
		"df4_ct":             regs.ReadDbg(r, regs.DbgDf4Ct),
		"df5_ct":             regs.ReadDbg(r, regs.DbgDf5Ct),
		"df11_ct":            regs.ReadDbg(r, regs.DbgDf11Ct),
		"df17_ct":            regs.ReadDbg(r, regs.DbgDf17Ct),
		"df18_ct":            regs.ReadDbg(r, regs.DbgDf18Ct),
		"msg_ct":             regs.ReadDbg(r, regs.DbgMsgCt),
		"agg_valid_ct":       regs.ReadDbg(r, regs.DbgAggValidCt),
		"agg_drop_ct":        regs.ReadDbg(r, regs.DbgAggDropCt),
		"fifo_wr_ct":         regs.ReadDbg(r, regs.DbgFifoWrCt),
		"sample_fifo_ovf_ct": regs.ReadDbg(r, regs.DbgSampleFifoOvfCt),
	}
	if deepDebug {
		debugCounters["deep_debug"] = 1
		debugCounters["rx_valid_ct"] = regs.ReadDbg(r, regs.DbgRxValidCt)
		debugCounters["smp_valid_ct"] = regs.ReadDbg(r, regs.DbgSmpValidCt)
		debugCounters["edge_shape_ct"] = regs.ReadDbg(r, regs.DbgEdgeShapeCt)
		debugCounters["edge_qual_ct"] = regs.ReadDbg(r, regs.DbgEdgeQualCt)
		debugCounters["pre_qa_fail_ct"] = regs.ReadDbg(r, regs.DbgPreQaFailCt)
		debugCounters["pre_qb_fail_ct"] = regs.ReadDbg(r, regs.DbgPreQbFailCt)
		debugCounters["pre_qc_fail_ct"] = regs.ReadDbg(r, regs.DbgPreQcFailCt)
		debugCounters["pre_qd_fail_ct"] = regs.ReadDbg(r, regs.DbgPreQdFailCt)
		debugCounters["pre_holdoff_ct"] = regs.ReadDbg(r, regs.DbgPreHoldoffCt)
		debugCounters["pre_peak_age"] = regs.ReadDbg(r, regs.DbgPrePeakAge)
		debugCounters["pre_nofree_ct"] = regs.ReadDbg(r, regs.DbgPreNoFreeCt)
		debugCounters["pre_busy_drop_ct"] = regs.ReadDbg(r, regs.DbgPreBusyDropCt)
		debugCounters["dec_busy_max"] = regs.ReadDbg(r, regs.DbgDecBusyMax)
		debugCounters["smallest_done_ct"] = regs.ReadDbg(r, regs.DbgSmallestDoneCt)
		debugCounters["crc_attempt_ct"] = regs.ReadDbg(r, regs.DbgCrcAttemptCt)
		debugCounters["crc_exhaust_ct"] = regs.ReadDbg(r, regs.DbgCrcExhaustCt)
		debugCounters["cand_df4_ct"] = regs.ReadDbg(r, regs.DbgCandDf4Ct)
		debugCounters["cand_df5_ct"] = regs.ReadDbg(r, regs.DbgCandDf5Ct)
		debugCounters["cand_df11_ct"] = regs.ReadDbg(r, regs.DbgCandDf11Ct)
	}
	return debugCounters
}

// queryGpsdPosition connects to gpsd on localhost and retrieves the receiver
// position (latitude, longitude, altitude MSL). Returns an error if gpsd is
// unreachable or has no fix.
func queryGpsdPosition() (lat, lon, alt float64, err error) {
	conn, err := net.DialTimeout("tcp", "localhost:2947", 2*time.Second)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("connect gpsd: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send the WATCH command to start receiving TPV reports.
	if _, err := fmt.Fprintf(conn, "?WATCH={\"enable\":true,\"json\":true}\n"); err != nil {
		return 0, 0, 0, fmt.Errorf("write WATCH: %w", err)
	}

	// Read messages until we get a TPV with a fix.
	dec := json.NewDecoder(conn)
	for i := 0; i < 20; i++ {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, 0, 0, fmt.Errorf("read gpsd: %w", err)
		}
		var msg struct {
			Class  string  `json:"class"`
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			AltMSL float64 `json:"altMSL"`
			Mode   int     `json:"mode"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Class == "TPV" && msg.Mode >= 2 {
			return msg.Lat, msg.Lon, msg.AltMSL, nil
		}
	}
	return 0, 0, 0, fmt.Errorf("no TPV fix after 20 messages")
}

const (
	defaultBaseAddr          = 0x43D00000
	defaultBeastPort         = 30005
	defaultHTTPPort          = 8080
	defaultReorderWindow     = 75 * time.Millisecond
	defaultReorderMaxBuffered = 256
	defaultGainDB            = "28"
	defaultGainMode          = "manual"
	defaultQuietScoreShift = 6
	defaultSnrRatioShift   = 4
	defaultHoldoff         = 512
	defaultAutoGainMinDB     = 20.0
	defaultAutoGainMaxDB     = 30.0
	defaultAutoGainStepDownDB = 1.0
	defaultAutoGainStepUpDB   = 1.0
	defaultAutoGainHot75      = 1
	defaultAutoGainHot87      = 1
	defaultAutoGainHotHold    = 2
	defaultAutoGainCalmHold   = 12
	statusInterval           = 1 * time.Second
	positionInterval         = 30 * time.Second
	softResetSettleTime      = 5 * time.Millisecond
	idlePollSleep            = 100 * time.Microsecond
	defaultTrackChanDepth    = 256
	defaultDecoderClockHz    = 100_000_000
	defaultGPSDRetryInterval = 10 * time.Second
)

// receiverPosition holds the static receiver coordinates.
type receiverPosition struct {
	Lat float64
	Lon float64
	Alt float32
}

// radarcapeState tracks the Radarcape protocol state machine. It determines
// the output mode from PPS/GPS state, and emits 0x34 status and 0x35 position
// control frames at the appropriate times.
type radarcapeState struct {
	currentMode  beast.OutputMode
	desiredMode  beast.OutputMode
	lastGoodRef  *pps.ClockRef
	lastStatus   time.Time
	lastPosition time.Time
	position     *receiverPosition
	initialised  bool
}

type gainGuard struct {
	enabled          bool
	rd               *radio.Radio
	minGainDB        float64
	maxGainDB        float64
	stepDownDB       float64
	stepUpDB         float64
	hot87Threshold   uint32
	hot75Threshold   uint32
	hotHoldIntervals int
	calmHoldIntervals int
	hotStreak        int
	calmStreak       int
	last75           uint32
	last87           uint32
	lastNear         uint32
	lastSat          uint32
	primed           bool
}

func (g *gainGuard) Observe(now time.Time, currentGainDB float64, gainMode string, dbg map[string]uint32) {
	if !g.enabled || g.rd == nil {
		return
	}
	if gainMode != "manual" {
		g.hotStreak = 0
		g.calmStreak = 0
		return
	}

	cur75 := dbg["raw_iq_75pct_ct"]
	cur87 := dbg["raw_iq_87p5pct_ct"]
	curNear := dbg["raw_iq_nearrail_ct"]
	curSat := dbg["raw_power_sat_ct"]
	if !g.primed {
		g.last75, g.last87, g.lastNear, g.lastSat = cur75, cur87, curNear, curSat
		g.primed = true
		return
	}

	d75 := saturatingDelta(cur75, g.last75)
	d87 := saturatingDelta(cur87, g.last87)
	dNear := saturatingDelta(curNear, g.lastNear)
	dSat := saturatingDelta(curSat, g.lastSat)
	g.last75, g.last87, g.lastNear, g.lastSat = cur75, cur87, curNear, curSat

	overdriven := dNear > 0 || dSat > 0
	hot := overdriven || d87 >= g.hot87Threshold || d75 >= g.hot75Threshold
	calm := d75 == 0 && d87 == 0 && dNear == 0 && dSat == 0

	if hot {
		g.hotStreak++
		g.calmStreak = 0
	} else if calm {
		g.calmStreak++
		g.hotStreak = 0
	} else {
		g.hotStreak = 0
		g.calmStreak = 0
	}

	if g.hotStreak >= g.hotHoldIntervals {
		next := maxFloat(g.minGainDB, currentGainDB-g.stepDownDB)
		if next < currentGainDB {
			if err := g.rd.SetGain(formatWholeGain(next)); err != nil {
				log.Printf("WARNING: gain guard decrease %.0f -> %.0f dB failed: %v", currentGainDB, next, err)
			} else {
				log.Printf("gain guard: hot frontend (d75=%d d87=%d near=%d sat=%d), lowering gain %.0f -> %.0f dB",
					d75, d87, dNear, dSat, currentGainDB, next)
			}
		}
		g.hotStreak = 0
		g.calmStreak = 0
		return
	}

	if g.calmStreak >= g.calmHoldIntervals {
		next := minFloat(g.maxGainDB, currentGainDB+g.stepUpDB)
		if next > currentGainDB {
			if err := g.rd.SetGain(formatWholeGain(next)); err != nil {
				log.Printf("WARNING: gain guard increase %.0f -> %.0f dB failed: %v", currentGainDB, next, err)
			} else {
				log.Printf("gain guard: calm frontend for %d intervals, raising gain %.0f -> %.0f dB",
					g.calmHoldIntervals, currentGainDB, next)
			}
		}
		g.hotStreak = 0
		g.calmStreak = 0
	}
}

func saturatingDelta(cur, prev uint32) uint32 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

func parseGainDB(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimSuffix(s, "dB"))
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

func formatWholeGain(v float64) string {
	return fmt.Sprintf("%.0f", v)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// update computes the desired mode from the current clock reference.
func (rs *radarcapeState) update(ref *pps.ClockRef) {
	if ref != nil && ref.GpsSync {
		rs.desiredMode = beast.ModeRadarcapeGPS
		rs.lastGoodRef = ref
	} else {
		rs.desiredMode = beast.ModeRadarcapeLegacy12MHz
	}
}

// emitControlFrames returns any status/position frames that should be broadcast
// before the next data frame. now is the current wall clock (injected for testability).
func (rs *radarcapeState) emitControlFrames(now time.Time, toa uint64, ref *pps.ClockRef) [][]byte {
	var frames [][]byte

	if !rs.initialised {
		// Startup: emit initial status and position.
		statusFrame := beast.EncodeStatus(rs.desiredMode, rs.desiredMode, toa, ref, 0)
		frames = append(frames, statusFrame)
		if rs.position != nil {
			frames = append(frames, beast.EncodePosition(rs.position.Lat, rs.position.Lon, rs.position.Alt))
		}
		rs.currentMode = rs.desiredMode
		rs.lastStatus = now
		rs.lastPosition = now
		rs.initialised = true
		return frames
	}

	// Mode transition: timestamp in old mode, settings advertise the new mode.
	if rs.desiredMode != rs.currentMode {
		tsMode := rs.currentMode
		tsRef := ref
		// For GPS-to-legacy transitions, use the lastGoodRef for the old-mode timestamp.
		// The transition frame only needs to be parseable, not temporally accurate.
		if tsMode == beast.ModeRadarcapeGPS && (ref == nil || !ref.GpsSync) {
			tsRef = rs.lastGoodRef
		}
		statusFrame := beast.EncodeStatus(tsMode, rs.desiredMode, toa, tsRef, 0)
		frames = append(frames, statusFrame)
		rs.currentMode = rs.desiredMode
		rs.lastStatus = now
		return frames
	}

	// Periodic status.
	if now.Sub(rs.lastStatus) >= statusInterval {
		statusFrame := beast.EncodeStatus(rs.currentMode, rs.currentMode, toa, ref, 0)
		frames = append(frames, statusFrame)
		rs.lastStatus = now
	}

	// Periodic position.
	if rs.position != nil && now.Sub(rs.lastPosition) >= positionInterval {
		frames = append(frames, beast.EncodePosition(rs.position.Lat, rs.position.Lon, rs.position.Alt))
		rs.lastPosition = now
	}

	return frames
}

// welcomeFrames returns pre-encoded frames for late-joining clients.
func (rs *radarcapeState) welcomeFrames(toa uint64, ref *pps.ClockRef) [][]byte {
	var frames [][]byte
	frames = append(frames, beast.EncodeStatus(rs.currentMode, rs.currentMode, toa, ref, 0))
	if rs.position != nil {
		frames = append(frames, beast.EncodePosition(rs.position.Lat, rs.position.Lon, rs.position.Alt))
	}
	return frames
}

func main() {
	baseAddr := flag.Uint64("base-addr", defaultBaseAddr, "AXI register base address")
	port := flag.Int("port", defaultBeastPort, "Beast output TCP port")
	httpPort := flag.Int("http-port", defaultHTTPPort, "Web dashboard port")
	radarcape := flag.Bool("radarcape", false, "Use Radarcape timestamp format (UTC via PPS + NTP)")
	reorderWindow := flag.Duration("reorder-window", defaultReorderWindow, "Maximum TOA reordering hold window")
	reorderMaxBuffered := flag.Int("reorder-max-buffered", defaultReorderMaxBuffered, "Maximum buffered messages before forcing ordered flush")
	gain := flag.String("gain", defaultGainDB, "AD9361 RX gain in dB (initial manual tune)")
	gainMode := flag.String("gain-mode", defaultGainMode, "AD9361 RX gain control mode (manual, slow_attack, fast_attack, hybrid)")
	lat := flag.Float64("lat", 0, "Receiver latitude for CPR decode (0 = query gpsd)")
	lon := flag.Float64("lon", 0, "Receiver longitude for CPR decode (0 = query gpsd)")
	alt := flag.Float64("alt", 0, "Receiver altitude in metres (required with --lat/--lon for 0x35 position frames)")
	gpsdRetryInterval := flag.Duration("gpsd-retry-interval", defaultGPSDRetryInterval, "Retry interval for gpsd receiver position lookup when --lat/--lon are unset")
	quietScoreShift := flag.Uint("quiet-score-shift", defaultQuietScoreShift, "Initial quiet_score_shift detector setting")
	snrRatioShift := flag.Uint("snr-ratio-shift", defaultSnrRatioShift, "Initial snr_ratio_shift detector setting")
	holdoff := flag.Uint("holdoff", defaultHoldoff, "Initial preamble holdoff in samples (0-4095)")
	mock := flag.Bool("mock", false, "Use mock reader with empty FIFO (no hardware)")
	autoGainGuard := flag.Bool("auto-gain-guard", false, "Enable PS-side frontend headroom guard with hysteretic gain adjustments")
	autoGainMin := flag.Float64("auto-gain-min", defaultAutoGainMinDB, "Minimum manual gain in dB for --auto-gain-guard")
	autoGainMax := flag.Float64("auto-gain-max", defaultAutoGainMaxDB, "Maximum manual gain in dB for --auto-gain-guard")
	autoGainStepDown := flag.Float64("auto-gain-step-down", defaultAutoGainStepDownDB, "Gain step down in dB when the frontend stays hot")
	autoGainStepUp := flag.Float64("auto-gain-step-up", defaultAutoGainStepUpDB, "Gain step up in dB when the frontend stays calm")
	autoGainHot75 := flag.Uint("auto-gain-hot75", defaultAutoGainHot75, "raw_iq_75pct delta threshold per guard interval that counts as hot")
	autoGainHot87 := flag.Uint("auto-gain-hot87", defaultAutoGainHot87, "raw_iq_87p5pct delta threshold per guard interval that counts as hot")
	autoGainHotHold := flag.Int("auto-gain-hot-hold", defaultAutoGainHotHold, "Consecutive hot guard intervals before lowering gain")
	autoGainCalmHold := flag.Int("auto-gain-calm-hold", defaultAutoGainCalmHold, "Consecutive calm guard intervals before raising gain")
	flag.Parse()

	// If no position specified, try to get it from gpsd.
	if *lat == 0 && *lon == 0 {
		if gLat, gLon, gAlt, err := queryGpsdPosition(); err == nil {
			*lat = gLat
			*lon = gLon
			if *alt == 0 {
				*alt = gAlt
			}
			log.Printf("receiver position from gpsd: %.4f, %.4f, alt=%.1fm", *lat, *lon, *alt)
		} else {
			log.Printf("WARNING: no receiver position (gpsd: %v). CPR decode and distance will be unavailable.", err)
		}
	}

	// --- 1. Open registers ---
	var reader regs.RegisterReader
	if *mock {
		log.Printf("using mock reader (empty FIFO, for testing)")
		reader = regs.NewMockReader()
	} else {
		if runtime.GOOS != "linux" {
			log.Fatalf("/dev/mem requires Linux (running on %s); use --mock for testing", runtime.GOOS)
		}
		var err error
		reader, err = regs.NewMemReader(*baseAddr)
		if err != nil {
			log.Fatalf("open registers: %v", err)
		}
		log.Printf("registers mapped at 0x%08X", *baseAddr)
	}
	defer reader.Close()

	// --- 2. Validate FPGA (fatal if dead) ---
	ver := reader.Read32(regs.RegVersion)
	if !*mock && (ver == 0x00000000 || ver == 0xFFFFFFFF) {
		log.Fatalf("FPGA not responding (version register = 0x%08X)", ver)
	}
	deepDebug := ver&regs.VersionDeepDebugFlag != 0
	versionWord := ver &^ regs.VersionDeepDebugFlag
	buildID := ver & 0xFFFF
	log.Printf("hardware version: %d.%d build=0x%04X dirty=%v",
		versionWord>>16, (versionWord>>8)&0xFF, buildID, buildID&0x8000 != 0)
	log.Printf("deep debug build: %v", deepDebug)

	// --- 3. Tune radio (non-fatal on failure) ---
	var rd *radio.Radio
	if !*mock {
		var err error
		rd, err = radio.Open()
		if err != nil {
			log.Printf("WARNING: radio not found: %v", err)
		} else if err := rd.Tune(*gain); err != nil {
			log.Printf("WARNING: radio tune failed: %v", err)
		} else {
			log.Printf("radio tuned: 1090 MHz, gain=%s dB", *gain)
			if err := rd.SetGainMode(*gainMode); err != nil {
				log.Printf("WARNING: set gain mode %q failed: %v", *gainMode, err)
			} else {
				log.Printf("gain mode: %s", *gainMode)
			}
		}
	}

	// --- 4. Reset and enable decoder ---
	// Pulse soft reset to clear stale FIFO state (including sticky overflow flag).
	reader.Write32(regs.RegControl, regs.ControlSoftReset)
	time.Sleep(softResetSettleTime)
	reader.Write32(regs.RegControl, regs.ControlEnable)

	// Apply the current known-good detector defaults at startup so the live
	// operating point matches the tuned baseline after feeder restarts.
	dc := regs.NewConfigWriter(reader)
	if err := dc.SetQuietScoreShift(uint32(*quietScoreShift)); err != nil {
		log.Printf("WARNING: set quiet_score_shift default failed: %v", err)
	}
	if err := dc.SetSnrRatioShift(uint32(*snrRatioShift)); err != nil {
		log.Printf("WARNING: set snr_ratio_shift default failed: %v", err)
	}
	if err := dc.SetHoldoff(uint32(*holdoff)); err != nil {
		log.Printf("WARNING: set holdoff default failed: %v", err)
	}

	// --- 3b. PPS watcher ---
	var ppsWatcher *pps.Watcher
	if !*mock {
		chronyChecker := chrony.NewChecker("", "")
		ppsWatcher = pps.NewWatcher(reader, chronyChecker)
		ppsWatcher.Start()
		defer ppsWatcher.Stop()
		log.Printf("PPS watcher started (polling at 2 Hz, chrony check every 2s)")
	}

	// --- 5. Beast TCP server ---
	filter := icao.NewFilter(60 * time.Second)
	beastSrv := server.New(*port)

	// Radarcape state machine: initialise BEFORE starting the TCP listener
	// so welcome frames are cached for the first connecting client.
	var rcState *radarcapeState
	if *radarcape {
		rcState = &radarcapeState{}
		// TODO: 0x35 position frames disabled pending framing investigation.
		// mlat-client loses sync after receiving a 0x35 frame. Disable until
		// the root cause is found. Re-enable by removing this block guard.
		if false {
			// Only emit 0x35 position frames when all three coordinates are known.
			if *lat != 0 && *lon != 0 && *alt != 0 {
				rcState.position = &receiverPosition{Lat: *lat, Lon: *lon, Alt: float32(*alt)}
			} else if *lat != 0 || *lon != 0 || *alt != 0 {
				log.Printf("WARNING: incomplete position (lat=%.4f, lon=%.4f, alt=%.1f); 0x35 position frames will not be emitted. Set all three of --lat, --lon, --alt.", *lat, *lon, *alt)
			}
		}
		// Determine initial mode from the PPS watcher.
		var initRef *pps.ClockRef
		if ppsWatcher != nil {
			initRef = ppsWatcher.Ref()
		}
		rcState.update(initRef)

		// Build and cache startup welcome frames before the listener starts.
		// Use the PPS counter as the timestamp source for the startup status frame.
		// toa=0 with a large CounterAtPps would underflow the GPS nanos calculation,
		// so we use the counter at the last PPS edge (gives nanos=0, correct second).
		var startupToa uint64
		if initRef != nil {
			startupToa = initRef.CounterAtPps
		}
		startupFrames := rcState.emitControlFrames(time.Now(), startupToa, initRef)
		beastSrv.SetWelcomeFrames(startupFrames)
	}

	if err := beastSrv.Start(); err != nil {
		log.Fatalf("beast server: %v", err)
	}
	log.Printf("Beast output on %s (radarcape=%v)", beastSrv.Addr(), *radarcape)

	// --- 6. Tracker ---
	trk := tracker.New(*lat, *lon)
	trackCh := make(chan regs.Message, defaultTrackChanDepth)
	go trk.Run(trackCh)

	if *lat == 0 && *lon == 0 && *gpsdRetryInterval > 0 {
		go func() {
			ticker := time.NewTicker(*gpsdRetryInterval)
			defer ticker.Stop()
			for range ticker.C {
				gLat, gLon, _, err := queryGpsdPosition()
				if err != nil {
					log.Printf("gpsd retry: no receiver position yet: %v", err)
					continue
				}
				trk.SetRefPos(gLat, gLon)
				log.Printf("receiver position from gpsd retry: %.4f, %.4f", gLat, gLon)
				return
			}
		}()
	}

	// --- 7. Web server ---
	var msgCount, dropCount atomic.Uint64
	var msgRate, crcPassRate atomic.Int64
	rejectedFrames := diag.NewRejectedFrameBuffer(diag.DefaultRejectedFrameCapacity)
	ss := &statsSource{
		startTime:   time.Now(),
		reader:      reader,
		deepDebug:   deepDebug,
		filter:      filter,
		beastSrv:    beastSrv,
		rd:          rd,
		ppsWatcher:  ppsWatcher,
		msgCount:    &msgCount,
		dropCount:   &dropCount,
		msgRate:     &msgRate,
		crcPassRate: &crcPassRate,
	}
	var rc web.RadioController
	if rd != nil {
		rc = rd
	}
	webSrv := web.New(trk, ss, rc, dc, rejectedFrames)
	if err := webSrv.Start(*httpPort); err != nil {
		log.Fatalf("web server: %v", err)
	}
	log.Printf("web dashboard on %s", webSrv.Addr())

	reorderBuf := reorder.New(*reorderWindow, defaultDecoderClockHz, *reorderMaxBuffered)
	log.Printf("TOA reorder buffer enabled (window=%v, max_buffered=%d)", *reorderWindow, *reorderMaxBuffered)

	// --- 8. Signal handler ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var (
		lastStats       time.Time
		lastMsgSnap     uint64
		lastCrcPassSnap uint32
		lastReadAt      time.Time
	)
	guard := &gainGuard{
		enabled:           *autoGainGuard,
		rd:                rd,
		minGainDB:         *autoGainMin,
		maxGainDB:         *autoGainMax,
		stepDownDB:        *autoGainStepDown,
		stepUpDB:          *autoGainStepUp,
		hot75Threshold:    uint32(*autoGainHot75),
		hot87Threshold:    uint32(*autoGainHot87),
		hotHoldIntervals:  *autoGainHotHold,
		calmHoldIntervals: *autoGainCalmHold,
	}
	if guard.enabled {
		log.Printf("gain guard enabled: min=%.0f max=%.0f down=%.0f up=%.0f hot75>=%d hot87>=%d hot_hold=%d calm_hold=%d",
			guard.minGainDB, guard.maxGainDB, guard.stepDownDB, guard.stepUpDB,
			guard.hot75Threshold, guard.hot87Threshold, guard.hotHoldIntervals, guard.calmHoldIntervals)
	}

	emit := func(msg regs.Message, clockRef *pps.ClockRef) {
		now := time.Now()

		// Radarcape control frames: mode transitions, periodic status/position.
		if rcState != nil {
			rcState.update(clockRef)

			controlFrames := rcState.emitControlFrames(now, msg.TOA, clockRef)
			for _, f := range controlFrames {
				beastSrv.Broadcast(f)
			}

			// Update welcome frames for late joiners when control frames were emitted.
			if len(controlFrames) > 0 {
				beastSrv.SetWelcomeFrames(rcState.welcomeFrames(msg.TOA, clockRef))
			}
		}

		// Encode and broadcast the data frame.
		var frame []byte
		if rcState != nil {
			frame = beast.EncodeData(rcState.currentMode, msg, clockRef)
		} else {
			frame = beast.EncodeData(beast.ModeBeast12MHz, msg, nil)
		}
		beastSrv.Broadcast(frame)
		msgCount.Add(1)

		select {
		case trackCh <- msg:
		default:
		}
	}

	// --- 9. FIFO poll loop ---
	log.Printf("polling FIFO...")
	for {
		select {
		case <-sigCh:
			log.Printf("shutting down (%d messages forwarded, %d dropped)",
				msgCount.Load(), dropCount.Load())
			close(trackCh)
			beastSrv.Stop()
			webSrv.Stop()
			return
		default:
		}

		// Stats tick runs regardless of FIFO state so rate drops to zero
		// when traffic stops instead of displaying a stale value.
		if time.Since(lastStats) > 10*time.Second {
			filter.Expire()
			trk.Expire()
			curMsgCt := msgCount.Load()
			elapsed := time.Since(lastStats).Seconds()
			curCrcPass := regs.ReadDbg(reader, regs.DbgCrcPassCt)
			var dbg map[string]uint32
			if elapsed > 0 && lastStats != (time.Time{}) {
				r := float64(curMsgCt-lastMsgSnap) / elapsed
				msgRate.Store(int64(r * 10))
				cr := float64(curCrcPass-lastCrcPassSnap) / elapsed
				crcPassRate.Store(int64(cr * 10))
			}
			if guard.enabled && rd != nil {
				dbg = readDebugCounters(reader, deepDebug)
				status := rd.ReadStatus()
				if status.GainMode != "" {
					if gainNow, err := parseGainDB(status.GainDB); err != nil {
						log.Printf("WARNING: gain guard parse gain %q failed: %v", status.GainDB, err)
					} else {
						guard.Observe(time.Now(), gainNow, status.GainMode, dbg)
					}
				}
			}
			lastMsgSnap = curMsgCt
			lastCrcPassSnap = curCrcPass
			ppsSnap := regs.ReadPps(reader)
			statusSnap := reader.Read32(regs.RegStatus)
			log.Printf("stats: %d msgs, %d dropped, %d icaos, %d aircraft, %d clients, PPS=%d, overflow=%v",
				curMsgCt, dropCount.Load(), filter.Count(), trk.Count(),
				beastSrv.ClientCount(), ppsSnap.Count, statusSnap&regs.StatusOverflow != 0)
			lastStats = time.Now()
		}

		status := reader.Read32(regs.RegStatus)
		if status&regs.StatusNotEmpty == 0 {
			if reorderBuf.Len() > 0 && !lastReadAt.IsZero() && time.Since(lastReadAt) >= *reorderWindow {
				var clockRef *pps.ClockRef
				if ppsWatcher != nil {
					clockRef = ppsWatcher.Ref()
				}
				for _, msg := range reorderBuf.FlushAll() {
					emit(msg, clockRef)
				}
			}
			time.Sleep(idlePollSleep)
			continue
		}

		// Get latest PPS-disciplined clock reference from watcher.
		var clockRef *pps.ClockRef
		if ppsWatcher != nil {
			clockRef = ppsWatcher.Ref()
		}

		// Drain all available messages in a burst to keep up with FIFO fill rate.
		for {
			raw := regs.ReadMessage(reader)
			msg := raw.Decode()
			lastReadAt = time.Now()
			df := msg.DF()

			switch df {
			case 11, 17, 18:
				filter.Add(msg.ICAO())
			}

			switch df {
			case 0, 4, 5:
				// Reject degenerate short frames (phantom detections from noise)
				// before attempting the ICAO filter lookup.
				if isDegenerateShort(msg.Bytes[:msg.Len]) {
					rejectedFrames.Record(msg, diag.ReasonDegenerateShortFrame, 0)
					dropCount.Add(1)
					goto next
				}
				addr := crc.Checksum(msg.Bytes[:msg.Len], msg.Len*8)
				if !filter.Test(addr) {
					rejectedFrames.Record(msg, diag.ReasonICAOFilterMiss, addr)
					dropCount.Add(1)
					goto next
				}
			case 16, 20, 21:
				addr := crc.Checksum(msg.Bytes[:msg.Len], msg.Len*8)
				if !filter.Test(addr) {
					rejectedFrames.Record(msg, diag.ReasonICAOFilterMiss, addr)
					dropCount.Add(1)
					goto next
				}
			}

			{
				for _, ready := range reorderBuf.Push(msg) {
					emit(ready, clockRef)
				}
			}

		next:
			if reader.Read32(regs.RegStatus)&regs.StatusNotEmpty == 0 {
				break
			}
		}
	}
}

// isDegenerateShort returns true if a 7-byte short frame (DF0/4/5) has fewer
// than 4 non-zero bits in the payload bytes (indices 1-6). These near-zero
// payloads are phantom detections from the FPGA preamble detector firing on
// noise.
func isDegenerateShort(raw []byte) bool {
	if len(raw) != 7 {
		return false
	}
	bits := 0
	for _, b := range raw[1:] {
		for v := b; v != 0; v &= v - 1 {
			bits++
		}
	}
	return bits < 4
}
