package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSparklineWidth(t *testing.T) {
	got := sparkline([]float64{0, 1, 2, 3}, 12)
	if utf8.RuneCountInString(got) != 12 {
		t.Fatalf("rune count = %d, want 12", utf8.RuneCountInString(got))
	}
}

func TestSparklineZeroSeries(t *testing.T) {
	got := sparkline([]float64{0, 0, 0}, 8)
	if got != strings.Repeat("▁", 8) {
		t.Fatalf("sparkline = %q, want %q", got, strings.Repeat("▁", 8))
	}
}

func TestDeltaRate(t *testing.T) {
	got := deltaRate(uint64(120), uint64(100), 10, 0)
	if got != 2 {
		t.Fatalf("deltaRate = %v, want 2", got)
	}
}
