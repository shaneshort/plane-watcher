package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

type entry struct {
	msg regs.Message
}

const syntheticCounterHz = 100_000_000

// loadFile reads messages from either *hex; reference format or GHDL sim log.
func loadFile(path, format string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	switch format {
	case "ref":
		return parseRef(f)
	case "sim":
		return parseSim(f)
	default:
		return nil, fmt.Errorf("unknown format %q (use ref or sim)", format)
	}
}

// parseRef reads *hex; lines (dump1090 / scan_iq.py --ref output).
// Hex is in standard Mode-S byte order.
// No TOA/RPL available — uses synthetic sequential timestamps at the
// current FPGA counter rate so replay still exercises the live PS/PL contract.
func parseRef(f *os.File) ([]entry, error) {
	var entries []entry
	scanner := bufio.NewScanner(f)
	toa := uint64(syntheticCounterHz) // start at 1 second

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "*") || !strings.HasSuffix(line, ";") {
			continue
		}
		hexStr := line[1 : len(line)-1]
		msg, err := hexToMessage(hexStr, toa, 0x400000)
		if err != nil {
			continue
		}
		entries = append(entries, entry{msg: msg})
		toa += syntheticCounterHz // 1 second apart
	}
	return entries, scanner.Err()
}

// parseSim reads GHDL sim log DECODED MESSAGE lines.
// Format: *** DECODED MESSAGE [decoder N] #N: <hex> TOA=<n> RPL=<n>
// Hex is in FPGA byte order (swizzled) — needs reversal for Mode-S order.
var simPattern = regexp.MustCompile(
	`DECODED MESSAGE \[decoder \d+\] #\d+: ([0-9A-Fa-f]+) TOA=(\d+) RPL=(-?\d+)`)

func parseSim(f *os.File) ([]entry, error) {
	var entries []entry
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		m := simPattern.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		toa, _ := strconv.ParseUint(m[2], 10, 64)
		rpl, _ := strconv.ParseInt(m[3], 10, 32)
		if rpl < 0 {
			rpl = 0
		}

		msg, err := fpgaHexToMessage(m[1], toa, uint32(rpl))
		if err != nil {
			continue
		}
		entries = append(entries, entry{msg: msg})
	}
	return entries, scanner.Err()
}

// hexToMessage creates a Message from a Mode-S hex string (standard byte order).
func hexToMessage(hexStr string, toa uint64, rpl uint32) (regs.Message, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return regs.Message{}, err
	}

	var msg regs.Message
	msg.TOA = toa
	msg.RPL = rpl

	if len(b) == 7 {
		msg.Len = 7
	} else if len(b) == 14 {
		msg.Len = 14
	} else {
		return msg, fmt.Errorf("unexpected message length %d", len(b))
	}

	copy(msg.Bytes[:], b)
	return msg, nil
}

// fpgaHexToMessage creates a Message from FPGA-order hex (swizzled, needs byte reversal).
func fpgaHexToMessage(hexStr string, toa uint64, rpl uint32) (regs.Message, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return regs.Message{}, err
	}

	// Reverse bytes to get Mode-S order
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}

	var msg regs.Message
	msg.TOA = toa
	msg.RPL = rpl

	df := b[0] >> 3
	if df >= 16 {
		msg.Len = 14
	} else {
		msg.Len = 7
	}

	copy(msg.Bytes[:], b)
	return msg, nil
}
