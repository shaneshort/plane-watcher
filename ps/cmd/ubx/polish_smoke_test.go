package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/gps"
)

func TestPolishSmoke(t *testing.T) {
	tm := newTerm(os.Stdout)

	mkRpt := func(mode int) gps.StatusReport {
		tmodeRaw, _ := hex.DecodeString("02000000d641dff1bd23091dffa900ec220400002c010000d0070000")
		rpt := gps.StatusReport{
			Generation: gps.GenM8,
			MonVer: gps.MonVer{
				SwVersion:  "EXT CORE 3.01 (111141)",
				HwVersion:  "00080000",
				Extensions: []string{"ROM BASE 2.01 (75331)", "FWVER=TIM 1.10", "GPS;GLO;GAL;BDS"},
			},
			TMODE:     gps.TMODEPayload{Generation: gps.GenM8, Raw: tmodeRaw},
			TMODEMode: mode,
			SVIN: gps.SVINStatus{
				DurationSec: 301, Observations: 302, MeanAccMeters: 1.058,
				Valid: mode == 1, Active: false,
				MeanXCm: -237026858, MeanYCm: 487138237, MeanZCm: -335500801,
			},
			NAV5: gps.NAV5Summary{DynModel: 2, FixMode: 3, MinElev: 5, UtcStandard: 3},
			TP5: gps.TP5Summary{
				Active: true, LockGpsFreq: true, FreqPeriod: 1, PulseLenRatio: 500000,
				IsFreq: true, IsLength: true, AlignToTow: true, RisingAtTop: true, GridUTC: true,
			},
			Clock: gps.ClockStatus{
				ITOW: 234_567_000, ClockBiasNs: -3422, ClockDriftNsPerS: -12,
				TimeAccuracyNs: 8, FreqAccuracyPsPerS: 12500,
			},
			SKY: &gps.SKY{
				HDOP: 0.56, VDOP: 0.84, PDOP: 1.01, NSat: 23, USat: 20,
				Satellites: []gps.Satellite{
					{PRN: 85, GnssID: 6, Az: 31, El: 56, SS: 50, Used: true},
					{PRN: 24, GnssID: 0, Az: 20, El: 71, SS: 47, Used: true},
				},
			},
		}
		return rpt
	}

	t.Run("fixed-mode status", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusHuman(&buf, tm, mkRpt(2))
		t.Log("\n" + buf.String())
	})
}
