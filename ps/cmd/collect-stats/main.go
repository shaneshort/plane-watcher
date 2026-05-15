package main

import (
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

var fields = []string{
	"quiet_score_shift",
	"snr_ratio_shift",
	"holdoff",
	"raw_power_max",
	"power_max",
	"raw_power_thr_ct",
	"power_thr_ct",
	"raw_iq_75pct_ct",
	"raw_iq_87p5pct_ct",
	"raw_iq_nearrail_ct",
	"raw_power_sat_ct",
	"pre_abs_ct",
	"pre_quiet_ct",
	"pre_snr_ct",
	"pre_pass_ct",
	"pre_det_ct",
	"pre_peak_age",
	"invalid_df_ct",
	"crc_attempt_ct",
	"crc_pass_ct",
	"crc_exhaust_ct",
	"df4_ct",
	"df5_ct",
	"df11_ct",
	"df17_ct",
	"df18_ct",
	"msg_ct",
	"fifo_wr_ct",
	"agg_valid_ct",
	"agg_drop_ct",
	"drop_count",
}

func main() {
	baseURL := flag.String("base-url", "http://planewatcher.local:8080", "plane-feeder base URL")
	interval := flag.Duration("interval", 30*time.Second, "sample interval")
	count := flag.Int("count", 0, "number of samples to collect (0 = run forever)")
	outPath := flag.String("out", "", "CSV output path (default stdout)")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP timeout")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}

	out := os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fatalf("open output: %v", err)
		}
		defer f.Close()
		out = f
	}

	fmt.Fprintln(out, csvHeader())

	samples := 0
	var prev *web.StatsData
	for {
		now := time.Now()
		stats, err := getStats(client, *baseURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s stats read failed: %v\n", now.Format(time.RFC3339), err)
		} else {
			fmt.Fprintln(out, csvRow(now, stats, prev))
			fmt.Fprintf(os.Stderr,
				"%s sample=%d msg_count=%d msg_rate=%.1f drop_count=%d aircraft=%d quiet=%d snr=%d holdoff=%d\n",
				now.Format(time.RFC3339),
				samples+1,
				stats.MsgCount,
				stats.MsgRate,
				stats.DropCount,
				stats.AircraftCount,
				stats.Debug["quiet_score_shift"],
				stats.Debug["snr_ratio_shift"],
				stats.Debug["holdoff"],
			)
			statsCopy := stats
			prev = &statsCopy
		}

		samples++
		if *count > 0 && samples >= *count {
			return
		}
		time.Sleep(*interval)
	}
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

func csvHeader() string {
	cols := []string{
		"ts_unix_ns",
		"uptime_s",
		"msg_count",
		"msg_rate",
		"drop_count",
		"icao_count",
		"aircraft_count",
		"client_count",
		"pps_count",
		"overflow",
	}
	cols = append(cols, fields...)
	for _, k := range fields {
		cols = append(cols, k+"_delta")
	}
	return strings.Join(cols, ",")
}

func csvRow(now time.Time, s web.StatsData, prev *web.StatsData) string {
	cols := []string{
		strconv.FormatInt(now.UnixNano(), 10),
		strconv.FormatInt(s.Uptime, 10),
		strconv.FormatUint(s.MsgCount, 10),
		strconv.FormatFloat(s.MsgRate, 'f', 1, 64),
		strconv.FormatUint(s.DropCount, 10),
		strconv.Itoa(s.ICAOCount),
		strconv.Itoa(s.AircraftCount),
		strconv.Itoa(s.ClientCount),
		strconv.FormatUint(uint64(s.PPSCount), 10),
		strconv.FormatBool(s.Overflow),
	}
	for _, k := range fields {
		switch k {
		case "drop_count":
			cols = append(cols, strconv.FormatUint(s.DropCount, 10))
		default:
			cols = append(cols, strconv.FormatUint(uint64(s.Debug[k]), 10))
		}
	}
	for _, k := range fields {
		delta, ok := counterDelta(prev, &s, k)
		if !ok {
			cols = append(cols, "")
			continue
		}
		cols = append(cols, strconv.FormatUint(delta, 10))
	}
	return strings.Join(cols, ",")
}

func counterDelta(prev, cur *web.StatsData, key string) (uint64, bool) {
	curVal, ok := counterValue(cur, key)
	if !ok {
		return 0, false
	}
	if prev == nil {
		return 0, true
	}
	prevVal, ok := counterValue(prev, key)
	if !ok {
		return 0, true
	}
	if curVal < prevVal {
		return 0, true
	}
	return curVal - prevVal, true
}

func counterValue(s *web.StatsData, key string) (uint64, bool) {
	switch key {
	case "drop_count":
		return s.DropCount, true
	default:
		v, ok := s.Debug[key]
		return uint64(v), ok
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
