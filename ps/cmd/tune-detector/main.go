package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/web"
)

type detectorResp struct {
	QuietScoreShift uint32 `json:"quiet_score_shift"`
	SnrRatioShift   uint32 `json:"snr_ratio_shift"`
}

type aircraft struct {
	ICAO     icaoValue `json:"icao"`
	Messages uint64    `json:"messages"`
}

type icaoValue uint32

func (v *icaoValue) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		*v = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(s), "0X"))
		n, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return err
		}
		*v = icaoValue(n)
		return nil
	}
	var n uint32
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = icaoValue(n)
	return nil
}

func main() {
	baseURL := flag.String("base-url", "http://pluto.local:8080", "plane-feeder base URL")
	quietList := flag.String("quiet-list", "1", "comma-separated quiet_score_shift values to test")
	snrList := flag.String("snr-list", "0", "comma-separated snr_ratio_shift values to test")
	trackICAOs := flag.String("track-icaos", "", "comma-separated ICAOs to track from /api/aircraft (hex, e.g. 7CF62D,7CF67B)")
	settle := flag.Duration("settle", 5*time.Second, "settle time after applying a setting")
	sample := flag.Duration("sample", 15*time.Second, "measurement window per setting")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP timeout")
	restore := flag.Bool("restore", true, "restore the original detector settings when done")
	csvMode := flag.Bool("csv", false, "emit CSV rows instead of aligned text")
	flag.Parse()

	quietVals, err := parseUintList(*quietList)
	if err != nil {
		fatalf("parse quiet-list: %v", err)
	}
	snrVals, err := parseUintList(*snrList)
	if err != nil {
		fatalf("parse snr-list: %v", err)
	}
	trackedICAOs, err := parseICAOList(*trackICAOs)
	if err != nil {
		fatalf("parse track-icaos: %v", err)
	}

	client := &http.Client{Timeout: *timeout}

	initial, err := getStats(client, *baseURL)
	if err != nil {
		fatalf("read initial stats: %v", err)
	}
	origQuiet, ok := initial.Debug["quiet_score_shift"]
	if !ok {
		fatalf("initial stats missing debug.quiet_score_shift")
	}
	origSnr, ok := initial.Debug["snr_ratio_shift"]
	if !ok {
		fatalf("initial stats missing debug.snr_ratio_shift")
	}

	if *restore {
		defer func() {
			if err := setDetectorShift(client, *baseURL, "quiet-score-shift", origQuiet); err != nil {
				fmt.Fprintf(os.Stderr, "warning: restore quiet_score_shift=%d failed: %v\n", origQuiet, err)
			}
			if err := setDetectorShift(client, *baseURL, "snr-ratio-shift", origSnr); err != nil {
				fmt.Fprintf(os.Stderr, "warning: restore snr_ratio_shift=%d failed: %v\n", origSnr, err)
			}
		}()
	}

	if *csvMode {
		header := []string{
			"quiet_score_shift",
			"snr_ratio_shift",
			"msg_delta",
			"crc_pass_delta",
			"invalid_df_delta",
			"drop_delta",
			"pre_det_delta",
			"pre_pass_delta",
			"df4_delta",
			"df5_delta",
			"raw_iq_75pct_delta",
			"raw_iq_87p5pct_delta",
			"raw_iq_nearrail_delta",
			"raw_power_sat_delta",
			"raw_power_thr_delta",
			"power_thr_delta",
			"sample_fifo_ovf_delta",
			"icao_count",
			"aircraft_count",
			"overflow",
		}
		if len(trackedICAOs) > 0 {
			header = append(header, "tracked_sum_delta")
			for _, icao := range trackedICAOs {
				header = append(header, fmt.Sprintf("icao_%06X_delta", icao))
			}
		}
		fmt.Println(strings.Join(header, ","))
	} else {
		fmt.Printf("Detector sweep at %s\n", *baseURL)
		fmt.Printf("Original config: quiet_score_shift=%d snr_ratio_shift=%d\n", origQuiet, origSnr)
		fmt.Printf("Settle=%s sample=%s\n", settle.Round(time.Millisecond), sample.Round(time.Millisecond))
		fmt.Print("quiet snr msgs crc_pass invalid_df drops pre_det pre_pass df4 df5 iq75 iq87.5 nearrail pwr_sat raw_thr pwr_thr fifo_ovf icao aircraft note")
		if len(trackedICAOs) > 0 {
			fmt.Print(" tracked")
			for _, icao := range trackedICAOs {
				fmt.Printf(" %06X", icao)
			}
		}
		fmt.Println()
	}

	for _, q := range quietVals {
		for _, s := range snrVals {
			if err := setDetectorShift(client, *baseURL, "quiet-score-shift", q); err != nil {
				fatalf("set quiet_score_shift=%d: %v", q, err)
			}
			if err := setDetectorShift(client, *baseURL, "snr-ratio-shift", s); err != nil {
				fatalf("set snr_ratio_shift=%d: %v", s, err)
			}

			time.Sleep(*settle)

			start, err := getStats(client, *baseURL)
			if err != nil {
				fatalf("read start stats for q=%d s=%d: %v", q, s, err)
			}
			startTracked, err := getTrackedAircraft(client, *baseURL, trackedICAOs)
			if err != nil {
				fatalf("read start aircraft for q=%d s=%d: %v", q, s, err)
			}

			time.Sleep(*sample)

			end, err := getStats(client, *baseURL)
			if err != nil {
				fatalf("read end stats for q=%d s=%d: %v", q, s, err)
			}
			endTracked, err := getTrackedAircraft(client, *baseURL, trackedICAOs)
			if err != nil {
				fatalf("read end aircraft for q=%d s=%d: %v", q, s, err)
			}

			msgDelta := delta(end.MsgCount, start.MsgCount)
			dropDelta := delta(end.DropCount, start.DropCount)
			crcPassDelta := debugDelta(end, start, "crc_pass_ct")
			invalidDfDelta := debugDelta(end, start, "invalid_df_ct")
			preDetDelta := debugDelta(end, start, "pre_det_ct")
			prePassDelta := debugDelta(end, start, "pre_pass_ct")
			df4Delta := debugDelta(end, start, "df4_ct")
			df5Delta := debugDelta(end, start, "df5_ct")
			rawIQ75Delta := debugDelta(end, start, "raw_iq_75pct_ct")
			rawIQ87Delta := debugDelta(end, start, "raw_iq_87p5pct_ct")
			rawIQNearrailDelta := debugDelta(end, start, "raw_iq_nearrail_ct")
			rawPowerSatDelta := debugDelta(end, start, "raw_power_sat_ct")
			rawPowerThrDelta := debugDelta(end, start, "raw_power_thr_ct")
			powerThrDelta := debugDelta(end, start, "power_thr_ct")
			sampleFIFOOvfDelta := debugDelta(end, start, "sample_fifo_ovf_ct")
			trackedDeltas := trackedAircraftDeltas(startTracked, endTracked, trackedICAOs)
			trackedSum := uint64(0)
			for _, d := range trackedDeltas {
				trackedSum += d
			}

			note := ""
			if end.Overflow {
				note = "overflow"
			}

			if *csvMode {
				overflowVal := 0
				if end.Overflow {
					overflowVal = 1
				}
				fmt.Printf("%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d",
					q, s,
					msgDelta,
					crcPassDelta,
					invalidDfDelta,
					dropDelta,
					preDetDelta,
					prePassDelta,
					df4Delta,
					df5Delta,
					rawIQ75Delta,
					rawIQ87Delta,
					rawIQNearrailDelta,
					rawPowerSatDelta,
					rawPowerThrDelta,
					powerThrDelta,
					sampleFIFOOvfDelta,
					end.ICAOCount,
					end.AircraftCount,
					overflowVal)
				if len(trackedICAOs) > 0 {
					fmt.Printf(",%d", trackedSum)
					for _, icao := range trackedICAOs {
						fmt.Printf(",%d", trackedDeltas[icao])
					}
				}
				fmt.Println()
			} else {
				fmt.Printf("%5d %3d %4d %8d %10d %5d %7d %8d %3d %3d %5d %6d %8d %7d %7d %7d %8d %8d %8d %s",
					q, s,
					msgDelta,
					crcPassDelta,
					invalidDfDelta,
					dropDelta,
					preDetDelta,
					prePassDelta,
					df4Delta,
					df5Delta,
					rawIQ75Delta,
					rawIQ87Delta,
					rawIQNearrailDelta,
					rawPowerSatDelta,
					rawPowerThrDelta,
					powerThrDelta,
					sampleFIFOOvfDelta,
					end.ICAOCount,
					end.AircraftCount,
					note)
				if len(trackedICAOs) > 0 {
					fmt.Printf(" tracked=%d", trackedSum)
					for _, icao := range trackedICAOs {
						fmt.Printf(" %06X=%d", icao, trackedDeltas[icao])
					}
				}
				fmt.Println()
			}
		}
	}
}

func parseICAOList(s string) ([]uint32, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	vals := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(p), "0X"))
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", p, err)
		}
		vals = append(vals, uint32(v))
	}
	return vals, nil
}

func parseUintList(s string) ([]uint32, error) {
	parts := strings.Split(s, ",")
	vals := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", p, err)
		}
		vals = append(vals, uint32(v))
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("empty list")
	}
	return vals, nil
}

func getStats(client *http.Client, baseURL string) (web.StatsData, error) {
	var data web.StatsData
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/api/stats?debug=1")
	if err != nil {
		return data, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return data, fmt.Errorf("status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return data, err
	}
	return data, nil
}

func getTrackedAircraft(client *http.Client, baseURL string, tracked []uint32) (map[uint32]uint64, error) {
	out := make(map[uint32]uint64, len(tracked))
	if len(tracked) == 0 {
		return out, nil
	}

	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/api/aircraft")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}

	var list []aircraft
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	for _, icao := range tracked {
		out[icao] = 0
	}
	for _, ac := range list {
		icao := uint32(ac.ICAO)
		if _, ok := out[icao]; ok {
			out[icao] = ac.Messages
		}
	}
	return out, nil
}

func setDetectorShift(client *http.Client, baseURL, name string, val uint32) error {
	body, err := json.Marshal(map[string]uint32{"value": val})
	if err != nil {
		return err
	}
	url := strings.TrimRight(baseURL, "/") + "/api/detector/" + name
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	var out detectorResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	return nil
}

func delta[T ~uint64 | ~int64](end, start T) T {
	if end < start {
		return 0
	}
	return end - start
}

func debugDelta(end, start web.StatsData, key string) uint32 {
	e, eok := end.Debug[key]
	s, sok := start.Debug[key]
	if !eok || !sok || e < s {
		return 0
	}
	return e - s
}

func trackedAircraftDeltas(start, end map[uint32]uint64, tracked []uint32) map[uint32]uint64 {
	out := make(map[uint32]uint64, len(tracked))
	for _, icao := range tracked {
		s := start[icao]
		e := end[icao]
		if e < s {
			out[icao] = 0
		} else {
			out[icao] = e - s
		}
	}
	return out
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
