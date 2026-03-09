package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

type dbgEntry struct {
	index uint32
	name  string
	desc  string
	hex   bool // true = print as 0x%08X with bit decode
}

type dbgGroup struct {
	title string
	items []dbgEntry
}

var dbgGroups = []dbgGroup{
	{
		title: "Ingress",
		items: []dbgEntry{
			{regs.DbgRxValidCt, "RX_VALID_CT", "raw RX valid pulses into wrapper", false},
			{regs.DbgSmpValidCt, "SMP_VALID_CT", "scalar sample_valid pulses after ingress/downsampling", false},
			{regs.DbgRawPowerMax, "RAW_POWER_MAX", "peak scalar power before downsampling", false},
			{regs.DbgRawIq75PctCt, "RAW_IQ_75PCT_CT", "raw I/Q samples with either lane above 75% full scale", false},
			{regs.DbgRawIq87P5PctCt, "RAW_IQ_87P5PCT_CT", "raw I/Q samples with either lane above 87.5% full scale", false},
			{regs.DbgRawIqNearrailCt, "RAW_IQ_NEARRAIL_CT", "raw I/Q samples with either lane near full scale", false},
			{regs.DbgRawPowerSatCt, "RAW_POWER_SAT_CT", "pre-downsample scalar power samples near ceiling", false},
			{regs.DbgSampleFifoOvfCt, "SAMPLE_FIFO_OVF_CT", "samples dropped because the RX-to-core sample FIFO was full", false},
			{regs.DbgRawPowerThrCt, "RAW_POWER_THR_CT", "pre-downsample samples above main power threshold", false},
			{regs.DbgPowerMax, "POWER_MAX", "peak scalar power seen at decoder input", false},
			{regs.DbgEdgeThrCt, "EDGE_THR_CT", "samples above edge threshold", false},
			{regs.DbgPowerThrCt, "POWER_THR_CT", "samples above main power threshold", false},
		},
	},
	{
		title: "Core State",
		items: []dbgEntry{
			{regs.DbgCoreClkCt, "CORE_CLK_CT", "decoder core clock cycles observed", false},
			{regs.DbgCoreInValCt, "CORE_INVAL_CT", "in_valid pulses seen by decoder core", false},
			{regs.DbgCoreState, "CORE_STATE", "bit0=reset bit1=enable bit2=in_valid", true},
		},
	},
	{
		title: "Edge Detector",
		items: []dbgEntry{
			{regs.DbgEdgeShapeCt, "EDGE_SHAPE_CT", "edge-shape candidates before threshold qualification", false},
			{regs.DbgEdgeQualCt, "EDGE_QUAL_CT", "threshold-qualified edge windows", false},
			{regs.DbgEdgeCt, "EDGE_CT", "final edge detector outputs", false},
		},
	},
	{
		title: "Preamble",
		items: []dbgEntry{
			{regs.DbgPreAbsCt, "PRE_ABS_CT", "all four pulse-sum threshold checks passed", false},
			{regs.DbgPreQuietCt, "PRE_QUIET_CT", "quiet-zone checks passed after pulse threshold", false},
			{regs.DbgPreQaFailCt, "PRE_QA_FAIL_CT", "quiet-zone A failures after pulse threshold", false},
			{regs.DbgPreQbFailCt, "PRE_QB_FAIL_CT", "quiet-zone B failures after pulse threshold", false},
			{regs.DbgPreQcFailCt, "PRE_QC_FAIL_CT", "quiet-zone C failures after pulse threshold", false},
			{regs.DbgPreQdFailCt, "PRE_QD_FAIL_CT", "quiet-zone D failures after pulse threshold", false},
			{regs.DbgPrePeakAge, "PRE_PEAK_AGE", "qualifying-sample age of last fired preamble peak", false},
			{regs.DbgPreNoFreeCt, "PRE_NOFREE_CT", "preambles dropped because all decoders were busy", false},
			{regs.DbgPreBusyDropCt, "PRE_BUSY_DROP_CT", "pending decoder claims lost because target became busy", false},
			{regs.DbgDecBusyMax, "DEC_BUSY_MAX", "peak number of simultaneously busy decoders", false},
			{regs.DbgPreSnrCt, "PRE_SNR_CT", "aggregate SNR check passed after quiet checks", false},
			{regs.DbgPreHoldoffCt, "PRE_HOLDOFF_CT", "qualified preambles suppressed by holdoff", false},
			{regs.DbgPrePassCt, "PRE_PASS_CT", "preamble quality-gate passes", false},
			{regs.DbgPreDetCt, "PRE_DET_CT", "preamble detections after peak logic", false},
			{regs.DbgSomCt, "SOM_CT", "start-of-message assignments", false},
		},
	},
	{
		title: "Bit Flipper",
		items: []dbgEntry{
			{regs.DbgSmallestDoneCt, "SMALL_DONE_CT", "weakest-bit tracker completions", false},
			{regs.DbgInvalidDfCt, "INVALID_DF_CT", "candidate messages rejected before CRC", false},
			{regs.DbgCrcAttemptCt, "CRC_ATTEMPT_CT", "CRC candidate launches for DF11/DF17/DF18", false},
			{regs.DbgCrcPassCt, "CRC_PASS_CT", "CRC-valid DF11/DF17/DF18 outputs", false},
			{regs.DbgCrcExhaustCt, "CRC_EXHAUST_CT", "all 32 CRC combinations failed for DF11/DF17/DF18", false},
		},
	},
	{
		title: "Candidate DF Mix",
		items: []dbgEntry{
			{regs.DbgCandDf4Ct, "CAND_DF4_CT", "assembled pre-CRC candidates classified as DF4", false},
			{regs.DbgCandDf5Ct, "CAND_DF5_CT", "assembled pre-CRC candidates classified as DF5", false},
			{regs.DbgCandDf11Ct, "CAND_DF11_CT", "assembled pre-CRC candidates classified as DF11", false},
		},
	},
	{
		title: "DF Mix",
		items: []dbgEntry{
			{regs.DbgDf4Ct, "DF4_CT", "forwarded DF4 outputs", false},
			{regs.DbgDf5Ct, "DF5_CT", "forwarded DF5 outputs", false},
			{regs.DbgDf11Ct, "DF11_CT", "valid DF11 outputs", false},
			{regs.DbgDf17Ct, "DF17_CT", "valid DF17 outputs", false},
			{regs.DbgDf18Ct, "DF18_CT", "valid DF18 outputs", false},
		},
	},
	{
		title: "Decode / FIFO",
		items: []dbgEntry{
			{regs.DbgMsgCt, "MSG_CT", "decoded message-valid pulses", false},
			{regs.DbgAggValidCt, "AGG_VALID_CT", "aggregator output-valid pulses", false},
			{regs.DbgAggDropCt, "AGG_DROP_CT", "messages lost because an aggregator holding slot was already full", false},
			{regs.DbgFifoWrCt, "FIFO_WR_CT", "FIFO write strobes", false},
		},
	},
}

var csvDbgEntries = []dbgEntry{
	{regs.DbgRxValidCt, "RX_VALID_CT", "", false},
	{regs.DbgSmpValidCt, "SMP_VALID_CT", "", false},
	{regs.DbgRawPowerMax, "RAW_POWER_MAX", "", false},
	{regs.DbgRawIq75PctCt, "RAW_IQ_75PCT_CT", "", false},
	{regs.DbgRawIq87P5PctCt, "RAW_IQ_87P5PCT_CT", "", false},
	{regs.DbgRawIqNearrailCt, "RAW_IQ_NEARRAIL_CT", "", false},
	{regs.DbgRawPowerSatCt, "RAW_POWER_SAT_CT", "", false},
	{regs.DbgSampleFifoOvfCt, "SAMPLE_FIFO_OVF_CT", "", false},
	{regs.DbgRawPowerThrCt, "RAW_POWER_THR_CT", "", false},
	{regs.DbgPowerMax, "POWER_MAX", "", false},
	{regs.DbgEdgeThrCt, "EDGE_THR_CT", "", false},
	{regs.DbgPowerThrCt, "POWER_THR_CT", "", false},
	{regs.DbgEdgeShapeCt, "EDGE_SHAPE_CT", "", false},
	{regs.DbgEdgeQualCt, "EDGE_QUAL_CT", "", false},
	{regs.DbgEdgeCt, "EDGE_CT", "", false},
	{regs.DbgPreAbsCt, "PRE_ABS_CT", "", false},
	{regs.DbgPreQuietCt, "PRE_QUIET_CT", "", false},
	{regs.DbgPreSnrCt, "PRE_SNR_CT", "", false},
	{regs.DbgPreHoldoffCt, "PRE_HOLDOFF_CT", "", false},
	{regs.DbgPrePassCt, "PRE_PASS_CT", "", false},
	{regs.DbgPreDetCt, "PRE_DET_CT", "", false},
	{regs.DbgSomCt, "SOM_CT", "", false},
	{regs.DbgPreNoFreeCt, "PRE_NOFREE_CT", "", false},
	{regs.DbgPreBusyDropCt, "PRE_BUSY_DROP_CT", "", false},
	{regs.DbgDecBusyMax, "DEC_BUSY_MAX", "", false},
	{regs.DbgSmallestDoneCt, "SMALL_DONE_CT", "", false},
	{regs.DbgInvalidDfCt, "INVALID_DF_CT", "", false},
	{regs.DbgCrcAttemptCt, "CRC_ATTEMPT_CT", "", false},
	{regs.DbgCrcPassCt, "CRC_PASS_CT", "", false},
	{regs.DbgCrcExhaustCt, "CRC_EXHAUST_CT", "", false},
	{regs.DbgDf4Ct, "DF4_CT", "", false},
	{regs.DbgDf5Ct, "DF5_CT", "", false},
	{regs.DbgDf11Ct, "DF11_CT", "", false},
	{regs.DbgCandDf4Ct, "CAND_DF4_CT", "", false},
	{regs.DbgCandDf5Ct, "CAND_DF5_CT", "", false},
	{regs.DbgCandDf11Ct, "CAND_DF11_CT", "", false},
	{regs.DbgDf17Ct, "DF17_CT", "", false},
	{regs.DbgDf18Ct, "DF18_CT", "", false},
	{regs.DbgMsgCt, "MSG_CT", "", false},
	{regs.DbgAggValidCt, "AGG_VALID_CT", "", false},
	{regs.DbgAggDropCt, "AGG_DROP_CT", "", false},
	{regs.DbgFifoWrCt, "FIFO_WR_CT", "", false},
	{regs.DbgPrePeakAge, "PRE_PEAK_AGE", "", false},
}

func printDbgEntry(r regs.RegisterReader, e dbgEntry) {
	val := regs.ReadDbg(r, e.index)
	if e.hex {
		fmt.Printf("  %-14s  %-44s  %s\n", e.name, fmt.Sprintf("raw=0x%08X", val), e.desc)
		fmt.Printf("  %-14s  %-44v  %s\n", "", val&1 != 0, "reset")
		fmt.Printf("  %-14s  %-44v  %s\n", "", val&2 != 0, "enable")
		fmt.Printf("  %-14s  %-44v  %s\n", "", val&4 != 0, "in_valid")
		return
	}
	fmt.Printf("  %-14s  %-44d  %s\n", e.name, val, e.desc)
}

func readCandidateWords(r regs.RegisterReader) [4]uint32 {
	return [4]uint32{
		regs.ReadDbg(r, regs.DbgCrc0W0),
		regs.ReadDbg(r, regs.DbgCrc0W1),
		regs.ReadDbg(r, regs.DbgCrc0W2),
		regs.ReadDbg(r, regs.DbgCrc0W3),
	}
}

type radioAttr struct {
	label    string
	paths    []string
	expected string
}

type radioSetting struct {
	path  string
	value string
}

func readSysfsTrimmed(paths ...string) string {
	var lastErr error
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
		lastErr = err
	}
	return fmt.Sprintf("ERROR: %v", lastErr)
}

func printRadioSettings() {
	attrs := []radioAttr{
		{"RX_LO", []string{"/sys/bus/iio/devices/iio:device0/out_altvoltage0_RX_LO_frequency"}, "1090000000"},
		{"RX_BW", []string{
			"/sys/bus/iio/devices/iio:device0/in_voltage_rf_bandwidth",
			"/sys/bus/iio/devices/iio:device0/in_voltage0_rf_bandwidth",
		}, "2000000"},
		{"SAMP_RATE", []string{
			"/sys/bus/iio/devices/iio:device0/in_voltage_sampling_frequency",
			"/sys/bus/iio/devices/iio:device0/in_voltage0_sampling_frequency",
		}, "30720000"},
		{"GAIN_MODE", []string{"/sys/bus/iio/devices/iio:device0/in_voltage0_gain_control_mode"}, "manual/slow_attack"},
		{"GAIN_DB", []string{"/sys/bus/iio/devices/iio:device0/in_voltage0_hardwaregain"}, "(context dependent)"},
		{"RX_PORT", []string{"/sys/bus/iio/devices/iio:device0/in_voltage0_rf_port_select"}, "A_BALANCED"},
	}

	fmt.Println("Radio")
	for _, a := range attrs {
		got := readSysfsTrimmed(a.paths...)
		status := ""
		if !strings.HasPrefix(got, "ERROR:") {
			switch a.label {
			case "GAIN_MODE":
				if got != "manual" && got != "slow_attack" {
					status = "  unexpected"
				}
			case "GAIN_DB":
				// No hard fail here; we just print the current value.
			default:
				if got != a.expected {
					status = "  mismatch"
				}
			}
		}
		fmt.Printf("  %-14s  %-44s  expected=%s%s\n", a.label, got, a.expected, status)
	}
}

func writeSysfs(path string, value string) error {
	return os.WriteFile(path, []byte(value), 0o644)
}

func pulseControl(r regs.RegisterReader, bit uint32) {
	ctl := r.Read32(regs.RegControl)
	r.Write32(regs.RegControl, ctl|bit)
}

func triggerSnapshot(r regs.RegisterReader) {
	pulseControl(r, regs.ControlSnapshot)
	time.Sleep(1 * time.Millisecond)
}

func resetDecoder(r regs.RegisterReader) {
	pulseControl(r, regs.ControlSoftReset)
	time.Sleep(5 * time.Millisecond)
}

func readRadioValue(label string) string {
	switch label {
	case "RX_LO":
		return readSysfsTrimmed("/sys/bus/iio/devices/iio:device0/out_altvoltage0_RX_LO_frequency")
	case "RX_BW":
		return readSysfsTrimmed(
			"/sys/bus/iio/devices/iio:device0/in_voltage_rf_bandwidth",
			"/sys/bus/iio/devices/iio:device0/in_voltage0_rf_bandwidth",
		)
	case "SAMP_RATE":
		return readSysfsTrimmed(
			"/sys/bus/iio/devices/iio:device0/in_voltage_sampling_frequency",
			"/sys/bus/iio/devices/iio:device0/in_voltage0_sampling_frequency",
		)
	case "GAIN_MODE":
		return readSysfsTrimmed("/sys/bus/iio/devices/iio:device0/in_voltage0_gain_control_mode")
	case "GAIN_DB":
		return readSysfsTrimmed("/sys/bus/iio/devices/iio:device0/in_voltage0_hardwaregain")
	case "RSSI":
		return readSysfsTrimmed("/sys/bus/iio/devices/iio:device0/in_voltage0_rssi")
	default:
		return ""
	}
}

func csvHeader() string {
	cols := []string{
		"ts_unix_ns", "status", "not_empty", "full", "overflow", "fill",
		"rx_lo", "rx_bw", "samp_rate", "gain_mode", "gain_db", "rssi",
	}
	for _, e := range csvDbgEntries {
		cols = append(cols, strings.ToLower(e.name))
	}
	return strings.Join(cols, ",")
}

func csvRow(r regs.RegisterReader, now time.Time) string {
	status := r.Read32(regs.RegStatus)
	fillCount := (status >> 8) & 0x7F

	cols := []string{
		strconv.FormatInt(now.UnixNano(), 10),
		strconv.FormatUint(uint64(status), 10),
		strconv.FormatBool(status&regs.StatusNotEmpty != 0),
		strconv.FormatBool(status&regs.StatusFull != 0),
		strconv.FormatBool(status&regs.StatusOverflow != 0),
		strconv.FormatUint(uint64(fillCount), 10),
		readRadioValue("RX_LO"),
		readRadioValue("RX_BW"),
		readRadioValue("SAMP_RATE"),
		readRadioValue("GAIN_MODE"),
		readRadioValue("GAIN_DB"),
		readRadioValue("RSSI"),
	}

	for _, e := range csvDbgEntries {
		cols = append(cols, strconv.FormatUint(uint64(regs.ReadDbg(r, e.index)), 10))
	}
	return strings.Join(cols, ",")
}

func retuneRadio() error {
	settings := []radioSetting{
		{"/sys/bus/iio/devices/iio:device0/out_altvoltage0_RX_LO_frequency", "1090000000"},
		{"/sys/bus/iio/devices/iio:device0/in_voltage_rf_bandwidth", "2000000"},
		{"/sys/bus/iio/devices/iio:device0/in_voltage_sampling_frequency", "30720000"},
		{"/sys/bus/iio/devices/iio:device0/in_voltage0_gain_control_mode", "manual"},
		{"/sys/bus/iio/devices/iio:device0/in_voltage0_hardwaregain", "54"},
	}

	for _, s := range settings {
		if err := writeSysfs(s.path, s.value); err != nil {
			return fmt.Errorf("write %s=%s: %w", s.path, s.value, err)
		}
	}
	return nil
}

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43D00000, "AXI register base address")
	pop := flag.Bool("pop", false, "read RPL register (pops one FIFO entry)")
	reset := flag.Bool("reset", false, "pulse the decoder soft reset before dumping")
	retune := flag.Bool("retune", false, "set Pluto RX to 1090 MHz / 2 MHz BW / 30.72 MSPS / manual 60 dB before dumping")
	csvMode := flag.Bool("csv", false, "emit CSV metrics instead of the human-readable dump")
	snapshot := flag.Bool("snapshot", false, "force an immediate debug snapshot before reading counters (the FPGA also refreshes snapshots periodically)")
	interval := flag.Duration("interval", time.Second, "CSV sample interval")
	count := flag.Int("count", 1, "number of CSV samples to emit (<=0 means run forever)")
	flag.Parse()

	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "error: /dev/mem requires Linux (running on %s)\n", runtime.GOOS)
		os.Exit(1)
	}

	r, err := regs.NewMemReader(*baseAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer r.Close()

	if *retune {
		if err := retuneRadio(); err != nil {
			fmt.Fprintf(os.Stderr, "error: retune failed: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if *reset {
		resetDecoder(r)
	}

	if *csvMode {
		fmt.Println(csvHeader())
		emitted := 0
		for *count <= 0 || emitted < *count {
			if *snapshot {
				triggerSnapshot(r)
			}
			fmt.Println(csvRow(r, time.Now()))
			emitted++
			if *count > 0 && emitted >= *count {
				break
			}
			time.Sleep(*interval)
		}
		return
	}

	// Optional: latch a coherent sample-domain snapshot before reading the debug bank.
	if *snapshot {
		triggerSnapshot(r)
	}

	fmt.Printf("Register dump at base 0x%08X\n", *baseAddr)
	fmt.Println("─────────────────────────────────────────────────────")

	// VERSION
	ver := r.Read32(regs.RegVersion)
	buildID := ver & 0xFFFF
	fmt.Printf("VERSION     (0x30): 0x%08X  v%d.%d build=0x%04X dirty=%v\n",
		ver, ver>>16, (ver>>8)&0xFF, buildID, buildID&0x8000 != 0)

	// CONTROL
	ctl := r.Read32(regs.RegControl)
	fmt.Printf("CONTROL     (0x2C): 0x%08X  enable=%v reset=%v\n", ctl, ctl&2 != 0, ctl&1 != 0)

	// CONFIG
	cfg := r.Read32(regs.RegConfig)
	fmt.Printf("CONFIG      (0x3C): 0x%08X  quiet_score_shift=%d snr_ratio_shift=%d holdoff=%d\n",
		cfg,
		cfg&regs.ConfigQuietScoreShiftMask,
		(cfg&regs.ConfigSnrRatioShiftMask)>>3,
		(cfg&regs.ConfigHoldoffMask)>>regs.ConfigHoldoffShift)

	// STATUS
	status := r.Read32(regs.RegStatus)
	notEmpty := status&regs.StatusNotEmpty != 0
	full := status&regs.StatusFull != 0
	overflow := status&regs.StatusOverflow != 0
	fillCount := (status >> 8) & 0x7F
	fmt.Printf("STATUS      (0x1C): 0x%08X  not_empty=%v full=%v overflow=%v fill=%d\n",
		status, notEmpty, full, overflow, fillCount)

	// PPS
	pps := regs.ReadPps(r)
	fmt.Printf("PPS_COUNT   (0x20): %d\n", pps.Count)
	fmt.Printf("PPS_CTR     (0x24): 0x%016X  (%d)\n", pps.CounterAtPps(), pps.CounterAtPps())

	fmt.Println("─────────────────────────────────────────────────────")
	printRadioSettings()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println("Debug Pipeline")
	fmt.Println("  key            value                                         meaning")
	fmt.Println("  values below come from the FPGA's background debug snapshot")
	if *snapshot {
		fmt.Println("  this run also forced an immediate snapshot refresh")
	}
	for _, g := range dbgGroups {
		fmt.Printf("%s\n", g.title)
		for _, e := range g.items {
			printDbgEntry(r, e)
		}
	}
	cand := readCandidateWords(r)
	fmt.Printf("CRC0_CAND     %08X %08X %08X %08X  iter=0 candidate as seen by CRC\n",
		cand[3], cand[2], cand[1], cand[0])

	// Message data
	fmt.Println("─────────────────────────────────────────────────────")
	if !notEmpty {
		fmt.Println("FIFO empty — no message data to display")
		return
	}

	d0 := r.Read32(regs.RegMsgData0)
	d1 := r.Read32(regs.RegMsgData1)
	d2 := r.Read32(regs.RegMsgData2)
	d3 := r.Read32(regs.RegMsgData3)
	toaLo := r.Read32(regs.RegToaLo)
	toaHi := r.Read32(regs.RegToaHi)
	toa := uint64(toaHi)<<32 | uint64(toaLo)

	fmt.Printf("MSG_DATA    (0x00): %08X %08X %08X %08X\n", d3, d2, d1, d0)
	fmt.Printf("TOA         (0x10): %d  (0x%016X)\n", toa, toa)

	if *pop {
		rpl := r.Read32(regs.RegRPL)
		fmt.Printf("RPL         (0x18): %d  (0x%06X)  [FIFO popped]\n", rpl&0xFFFFFF, rpl&0xFFFFFF)

		// Decode and show the message for convenience
		raw := regs.RawMessage{
			Data:  [4]uint32{d0, d1, d2, d3},
			ToaLo: toaLo, ToaHi: toaHi, RPL: rpl,
		}
		msg := raw.Decode()
		fmt.Printf("Decoded     DF=%d  Len=%d  ", msg.Bytes[0]>>3, msg.Len)
		for i := 0; i < msg.Len; i++ {
			fmt.Printf("%02X", msg.Bytes[i])
		}
		fmt.Println()
	} else {
		fmt.Println("RPL         (0x18): [skipped — use --pop to read and pop FIFO]")
	}
}
