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

func main() {
	baseURL := flag.String("base-url", "http://pluto.local:8080", "plane-feeder base URL")
	gainList := flag.String("gains", "63,69,73", "comma-separated manual gain values in dB")
	settle := flag.Duration("settle", 10*time.Second, "settle time after applying a gain")
	sample := flag.Duration("sample", 30*time.Second, "measurement window per gain")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP timeout")
	restore := flag.Bool("restore", true, "restore the original manual gain when done")
	csvMode := flag.Bool("csv", false, "emit CSV rows instead of aligned text")
	flag.Parse()

	gains, err := parseGainList(*gainList)
	if err != nil {
		fatalf("parse gains: %v", err)
	}

	client := &http.Client{Timeout: *timeout}

	initial, err := getStats(client, *baseURL)
	if err != nil {
		fatalf("read initial stats: %v", err)
	}
	origGain := normalizeGain(initial.Radio.GainDB)
	if origGain == "" {
		origGain = "69"
	}
	if *restore {
		defer func() {
			if err := setGain(client, *baseURL, origGain); err != nil {
				fmt.Fprintf(os.Stderr, "warning: restore gain=%s failed: %v\n", origGain, err)
			}
		}()
	}

	if *csvMode {
		fmt.Println(strings.Join([]string{
			"gain_db",
			"msg_delta",
			"crc_pass_delta",
			"invalid_df_delta",
			"drop_delta",
			"pre_det_delta",
			"pre_pass_delta",
			"raw_iq_75pct_delta",
			"raw_iq_87p5pct_delta",
			"raw_iq_nearrail_delta",
			"raw_power_sat_delta",
			"raw_power_thr_delta",
			"power_thr_delta",
			"sample_fifo_ovf_delta",
			"aircraft_count",
			"overflow",
			"rssi",
		}, ","))
	} else {
		fmt.Printf("Gain sweep at %s\n", *baseURL)
		fmt.Printf("Original gain: %s dB\n", origGain)
		fmt.Printf("Settle=%s sample=%s\n", settle.Round(time.Millisecond), sample.Round(time.Millisecond))
		fmt.Println("gain msgs crc_pass invalid_df drops pre_det pre_pass iq75 iq87.5 nearrail pwr_sat raw_thr pwr_thr fifo_ovf aircraft overflow rssi")
	}

	for _, gain := range gains {
		if err := setGain(client, *baseURL, gain); err != nil {
			fatalf("set gain=%s: %v", gain, err)
		}

		time.Sleep(*settle)

		start, err := getStats(client, *baseURL)
		if err != nil {
			fatalf("read start stats for gain=%s: %v", gain, err)
		}

		time.Sleep(*sample)

		end, err := getStats(client, *baseURL)
		if err != nil {
			fatalf("read end stats for gain=%s: %v", gain, err)
		}

		msgDelta := delta(end.MsgCount, start.MsgCount)
		crcPassDelta := debugDelta(end, start, "crc_pass_ct")
		invalidDfDelta := debugDelta(end, start, "invalid_df_ct")
		dropDelta := delta(end.DropCount, start.DropCount)
		preDetDelta := debugDelta(end, start, "pre_det_ct")
		prePassDelta := debugDelta(end, start, "pre_pass_ct")
		rawIQ75Delta := debugDelta(end, start, "raw_iq_75pct_ct")
		rawIQ87Delta := debugDelta(end, start, "raw_iq_87p5pct_ct")
		rawIQNearrailDelta := debugDelta(end, start, "raw_iq_nearrail_ct")
		rawPowerSatDelta := debugDelta(end, start, "raw_power_sat_ct")
		rawPowerThrDelta := debugDelta(end, start, "raw_power_thr_ct")
		powerThrDelta := debugDelta(end, start, "power_thr_ct")
		sampleFIFOOvfDelta := debugDelta(end, start, "sample_fifo_ovf_ct")

		if *csvMode {
			overflowVal := 0
			if end.Overflow {
				overflowVal = 1
			}
			fmt.Printf("%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%q\n",
				gain,
				msgDelta,
				crcPassDelta,
				invalidDfDelta,
				dropDelta,
				preDetDelta,
				prePassDelta,
				rawIQ75Delta,
				rawIQ87Delta,
				rawIQNearrailDelta,
				rawPowerSatDelta,
				rawPowerThrDelta,
				powerThrDelta,
				sampleFIFOOvfDelta,
				end.AircraftCount,
				overflowVal,
				end.Radio.RSSI,
			)
		} else {
			fmt.Printf("%4s %4d %8d %10d %5d %7d %8d %5d %6d %8d %7d %7d %7d %8d %8d %8s %s\n",
				gain,
				msgDelta,
				crcPassDelta,
				invalidDfDelta,
				dropDelta,
				preDetDelta,
				prePassDelta,
				rawIQ75Delta,
				rawIQ87Delta,
				rawIQNearrailDelta,
				rawPowerSatDelta,
				rawPowerThrDelta,
				powerThrDelta,
				sampleFIFOOvfDelta,
				end.AircraftCount,
				boolWord(end.Overflow),
				end.Radio.RSSI,
			)
		}
	}
}

func parseGainList(s string) ([]string, error) {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = normalizeGain(p)
		if p == "" {
			continue
		}
		if _, err := strconv.ParseFloat(p, 64); err != nil {
			return nil, fmt.Errorf("%q: %w", p, err)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty list")
	}
	return out, nil
}

func normalizeGain(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if fields := strings.Fields(s); len(fields) > 0 {
		s = fields[0]
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "dB"), "db")
	return strings.TrimSpace(s)
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

func setGain(client *http.Client, baseURL, gain string) error {
	body, err := json.Marshal(map[string]string{"gain_db": gain})
	if err != nil {
		return err
	}
	resp, err := client.Post(strings.TrimRight(baseURL, "/")+"/api/radio/gain", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
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

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
