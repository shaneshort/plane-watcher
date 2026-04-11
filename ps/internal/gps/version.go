package gps

import (
	"bytes"
	"fmt"
	"strings"
)

// Generation identifies the u-blox module family. Different generations use
// different config message layouts for survey-in: M8 uses CFG-TMODE2 +
// TIM-SVIN, F9 uses CFG-TMODE3 + NAV-SVIN.
type Generation int

const (
	GenUnknown Generation = iota
	GenM8
	GenF9
)

func (g Generation) String() string {
	switch g {
	case GenM8:
		return "M8"
	case GenF9:
		return "F9"
	default:
		return "unknown"
	}
}

// MonVer is a parsed UBX-MON-VER response. The struct preserves the raw
// hwVersion/swVersion strings (the receiver pads them with zeros, which we
// trim) plus any optional extension strings.
//
// Layout from u-blox interface description:
//
//	30 bytes: swVersion (ASCII, zero-terminated/padded)
//	10 bytes: hwVersion (ASCII, zero-terminated/padded)
//	N * 30 bytes: extension strings (each zero-terminated/padded)
type MonVer struct {
	SwVersion  string
	HwVersion  string
	Extensions []string
}

// ParseMonVer decodes a UBX-MON-VER payload.
func ParseMonVer(payload []byte) (MonVer, error) {
	if len(payload) < 40 {
		return MonVer{}, fmt.Errorf("ubx: MON-VER payload too short: %d bytes (need >=40)", len(payload))
	}
	mv := MonVer{
		SwVersion: trimC(payload[0:30]),
		HwVersion: trimC(payload[30:40]),
	}
	rest := payload[40:]
	for len(rest) >= 30 {
		mv.Extensions = append(mv.Extensions, trimC(rest[0:30]))
		rest = rest[30:]
	}
	return mv, nil
}

// Generation returns the module generation inferred from the hwVersion field.
// Known mappings come directly from u-blox documentation:
//
//	"00080000" → M8 family (NEO-M8x, LEA-M8T, etc.)
//	"00190000" → F9 family (ZED-F9P, ZED-F9T, etc.)
//
// Anything else returns GenUnknown — the caller must refuse to send
// generation-specific config in that case.
func (mv MonVer) Generation() Generation {
	switch mv.HwVersion {
	case "00080000":
		return GenM8
	case "00190000":
		return GenF9
	}
	return GenUnknown
}

// String renders the MON-VER fields the way ubxtool does, suitable for
// printing under `ubx version` or in --debug logs.
func (mv MonVer) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "swVersion: %s\n", mv.SwVersion)
	fmt.Fprintf(&b, "hwVersion: %s (%s)\n", mv.HwVersion, mv.Generation())
	for _, ext := range mv.Extensions {
		fmt.Fprintf(&b, "extension: %s\n", ext)
	}
	return b.String()
}

// trimC trims a zero-padded C string returned by the receiver.
func trimC(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
