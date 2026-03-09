package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/beast"
)

// Golden fixture: sim-log line → parse → beast.EncodeV2() → exact bytes.
// This must produce the same Beast frame as the direct RawMessage path
// in ps/golden_test.go TestGoldenLongDF17 — proving the replay parser
// and the AXI register path are equivalent.

func TestGoldenSimLogReplay(t *testing.T) {
	// Same DF17 message as the direct golden test:
	// Mode-S: 8D75804B580FF2CF7E9BA6F701D0
	// FPGA hex (swizzled): D001F7A69B7ECFF20F584B80758D
	line := `*** DECODED MESSAGE [decoder 0] #1: D001F7A69B7ECFF20F584B80758D TOA=1181 RPL=753992` + "\n"
	f := writeTempFile(t, line)
	entries, err := parseSim(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	msg := entries[0].msg
	frame := beast.EncodeV2(msg, nil)

	// Must match TestGoldenLongDF17 in ps/golden_test.go exactly:
	// ts12 = floor(1181 * 12 / 100) = 141 = 0x00000000008D
	// signal = sqrt(753992 / 2621440) * 255 = 136 = 0x88
	// msg: 8D 75 80 4B 58 0F F2 CF 7E 9B A6 F7 01 D0
	want := mustHex("1A 33 00 00 00 00 00 8D 88 8D 75 80 4B 58 0F F2 CF 7E 9B A6 F7 01 D0")

	if !bytes.Equal(frame, want) {
		t.Errorf("sim-log replay golden frame mismatch\n got: %s\nwant: %s", fmtHex(frame), fmtHex(want))
	}
}

func mustHex(s string) []byte {
	compact := ""
	for _, c := range s {
		if c != ' ' {
			compact += string(c)
		}
	}
	b, err := hex.DecodeString(compact)
	if err != nil {
		panic(fmt.Sprintf("mustHex(%q): %v", s, err))
	}
	return b
}

func fmtHex(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " "
		}
		s += p
	}
	return s
}
