package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/web"
)

func TestCSVHeaderIncludesDeltaColumns(t *testing.T) {
	header := csvHeader()
	if !strings.Contains(header, "raw_iq_75pct_ct_delta") {
		t.Fatalf("header missing raw_iq_75pct_ct_delta: %s", header)
	}
	if !strings.Contains(header, "invalid_df_ct_delta") {
		t.Fatalf("header missing invalid_df_ct_delta: %s", header)
	}
}

func TestCSVRowComputesCounterDeltas(t *testing.T) {
	prev := &web.StatsData{
		DropCount: 4,
		Debug: map[string]uint32{
			"raw_iq_75pct_ct": 100,
			"invalid_df_ct":   7,
		},
	}
	cur := web.StatsData{
		DropCount: 9,
		Debug: map[string]uint32{
			"raw_iq_75pct_ct": 140,
			"invalid_df_ct":   10,
		},
	}

	row := csvRow(time.Unix(0, 0), cur, prev)
	headerCols := strings.Split(csvHeader(), ",")
	rowCols := strings.Split(row, ",")
	if len(headerCols) != len(rowCols) {
		t.Fatalf("header cols=%d row cols=%d", len(headerCols), len(rowCols))
	}

	check := func(name string, want uint64) {
		t.Helper()
		for i, col := range headerCols {
			if col != name {
				continue
			}
			got, err := strconv.ParseUint(rowCols[i], 10, 64)
			if err != nil {
				t.Fatalf("%s parse: %v", name, err)
			}
			if got != want {
				t.Fatalf("%s = %d, want %d", name, got, want)
			}
			return
		}
		t.Fatalf("column %q not found", name)
	}

	check("raw_iq_75pct_ct_delta", 40)
	check("invalid_df_ct_delta", 3)
	check("drop_count_delta", 5)
}
