package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestParseRefLong(t *testing.T) {
	f := writeTempFile(t, "*8D7C7C9B990C2591B8300469D16A;\n")
	entries, err := parseRef(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	msg := entries[0].msg
	got := strings.ToUpper(hex.EncodeToString(msg.Bytes[:msg.Len]))
	want := "8D7C7C9B990C2591B8300469D16A"
	if got != want {
		t.Errorf("bytes = %s, want %s", got, want)
	}
	if msg.Len != 14 {
		t.Errorf("Len = %d, want 14", msg.Len)
	}
	if msg.TOA != syntheticCounterHz {
		t.Errorf("TOA = %d, want %d", msg.TOA, syntheticCounterHz)
	}
}

func TestParseRefShort(t *testing.T) {
	f := writeTempFile(t, "*5D7CAD40AD54B8;\n")
	entries, err := parseRef(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	msg := entries[0].msg
	got := strings.ToUpper(hex.EncodeToString(msg.Bytes[:msg.Len]))
	if got != "5D7CAD40AD54B8" {
		t.Errorf("bytes = %s, want 5D7CAD40AD54B8", got)
	}
	if msg.Len != 7 {
		t.Errorf("Len = %d, want 7", msg.Len)
	}
}

func TestParseSimLong(t *testing.T) {
	// FPGA hex for 8D7C7C9B990C2591B8300469D16A is 6AD1690430B891250C999B7C7C8D
	line := `*** DECODED MESSAGE [decoder 0] #1: 6AD1690430B891250C999B7C7C8D TOA=993363 RPL=2073329` + "\n"
	f := writeTempFile(t, line)
	entries, err := parseSim(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	msg := entries[0].msg
	got := strings.ToUpper(hex.EncodeToString(msg.Bytes[:msg.Len]))
	want := "8D7C7C9B990C2591B8300469D16A"
	if got != want {
		t.Errorf("bytes = %s, want %s (after reversing FPGA byte order)", got, want)
	}
	if msg.Len != 14 {
		t.Errorf("Len = %d, want 14", msg.Len)
	}
	if msg.TOA != 993363 {
		t.Errorf("TOA = %d, want 993363", msg.TOA)
	}
	if msg.RPL != 2073329 {
		t.Errorf("RPL = %d, want 2073329", msg.RPL)
	}
}

func TestParseSimShort(t *testing.T) {
	// DF0 short message: Mode-S hex 02E19504A36500, FPGA hex 0065A30495E102
	line := `*** DECODED MESSAGE [decoder 0] #1: 00000000000000A365500495E102 TOA=1164 RPL=753992` + "\n"
	f := writeTempFile(t, line)
	entries, err := parseSim(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	msg := entries[0].msg
	if msg.Len != 7 {
		t.Errorf("Len = %d, want 7 (DF0)", msg.Len)
	}
	df := msg.Bytes[0] >> 3
	if df != 0 {
		t.Errorf("DF = %d, want 0", df)
	}
}

func TestParseRefSkipsGarbage(t *testing.T) {
	f := writeTempFile(t, "not a message\n*8D7C7C9B990C2591B8300469D16A;\n# comment\n")
	entries, err := parseRef(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1 (should skip non-matching lines)", len(entries))
	}
}

func TestParseRefSyntheticToaSpacing(t *testing.T) {
	f := writeTempFile(t, "*8D7C7C9B990C2591B8300469D16A;\n*5D7CAD40AD54B8;\n")
	entries, err := parseRef(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].msg.TOA != syntheticCounterHz {
		t.Fatalf("first TOA = %d, want %d", entries[0].msg.TOA, syntheticCounterHz)
	}
	if entries[1].msg.TOA != 2*syntheticCounterHz {
		t.Fatalf("second TOA = %d, want %d", entries[1].msg.TOA, 2*syntheticCounterHz)
	}
}

func writeTempFile(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.log")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Seek(0, 0)
	return f
}
