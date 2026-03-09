package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const escape = 0x1A

func main() {
	addr := flag.String("addr", "localhost:30005", "Beast server address")
	count := flag.Int("count", 0, "stop after N frames (0 = unlimited)")
	quiet := flag.Bool("quiet", false, "suppress per-frame output, show summary only")
	flag.Parse()

	conn, err := net.DialTimeout("tcp", *addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("Connected to %s\n", *addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		conn.Close()
	}()

	var (
		shortCount    int
		longCount     int
		statusCount   int
		positionCount int
		errCount      int
		start         = time.Now()
	)

	buf := make([]byte, 4096)
	var pending []byte

	for {
		n, err := conn.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
		}

		// Parse complete frames from pending
		for {
			frame, rest, ok := extractFrame(pending)
			if !ok {
				break
			}
			pending = rest

			total := shortCount + longCount + statusCount + positionCount + errCount
			switch {
			case len(frame) == 15 && frame[0] == 0x32: // short: type + 6ts + 1sig + 7msg
				shortCount++
				if !*quiet {
					printFrame(total+1, frame)
				}
			case len(frame) == 22 && frame[0] == 0x33: // long: type + 6ts + 1sig + 14msg
				longCount++
				if !*quiet {
					printFrame(total+1, frame)
				}
			case len(frame) == 22 && frame[0] == 0x34: // Radarcape status
				statusCount++
				if !*quiet {
					printStatusFrame(total+1, frame)
				}
			case len(frame) == 22 && frame[0] == 0x35: // Radarcape position
				positionCount++
				if !*quiet {
					printPositionFrame(total+1, frame)
				}
			default:
				errCount++
				if !*quiet {
					fmt.Printf("#%d  ERROR  len=%d type=0x%02X\n", total+1, len(frame), frame[0])
				}
			}

			if *count > 0 && shortCount+longCount+statusCount+positionCount >= *count {
				printSummary(shortCount, longCount, statusCount, positionCount, errCount, time.Since(start))
				return
			}
		}

		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "\nread error: %v\n", err)
			}
			break
		}
	}

	printSummary(shortCount, longCount, statusCount, positionCount, errCount, time.Since(start))
}

// extractFrame finds the next complete Beast frame in buf.
// Returns the unescaped payload (without leading 0x1A), remaining buf, and ok.
func extractFrame(buf []byte) ([]byte, []byte, bool) {
	// Find leading 0x1A
	start := -1
	for i := 0; i < len(buf); i++ {
		if buf[i] == escape {
			if i+1 < len(buf) && buf[i+1] == escape {
				i++ // skip escaped pair
				continue
			}
			start = i
			break
		}
	}
	if start < 0 || start+2 >= len(buf) {
		return nil, buf, false
	}

	// Determine expected length from type byte (next byte after 0x1A, unescaped)
	typeIdx := start + 1
	if buf[typeIdx] == escape {
		return nil, buf, false // malformed
	}

	var payloadLen int
	switch buf[typeIdx] {
	case 0x32:
		payloadLen = 1 + 6 + 1 + 7 // type + ts + sig + short msg
	case 0x33:
		payloadLen = 1 + 6 + 1 + 14 // type + ts + sig + long msg
	case 0x34:
		payloadLen = 1 + 6 + 1 + 14 // type + ts + sig + status msg
	case 0x35:
		payloadLen = 1 + 21 // type + position data (no ts/sig)
	default:
		// Unknown type — skip this 0x1A and try again
		return nil, buf[start+1:], false
	}

	// Unescape payload bytes
	payload := make([]byte, 0, payloadLen)
	i := typeIdx
	for len(payload) < payloadLen {
		if i >= len(buf) {
			return nil, buf, false // need more data
		}
		if buf[i] == escape && i+1 < len(buf) && buf[i+1] == escape {
			payload = append(payload, escape)
			i += 2
		} else if buf[i] == escape && len(payload) > 0 {
			// Hit next frame start before completing this one
			break
		} else {
			payload = append(payload, buf[i])
			i++
		}
	}

	if len(payload) < payloadLen {
		return nil, buf, false // incomplete
	}

	return payload, buf[i:], true
}

func printFrame(num int, payload []byte) {
	typeName := "SHORT"
	msgLen := 7
	if payload[0] == 0x33 {
		typeName = "LONG "
		msgLen = 14
	}

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	signal := payload[7]
	msgHex := strings.ToUpper(hex.EncodeToString(payload[8 : 8+msgLen]))
	df := payload[8] >> 3

	fmt.Printf("#%-5d %s  DF=%-2d  TS=%-14d  SIG=%-3d  %s\n",
		num, typeName, df, ts, signal, msgHex)
}

// printStatusFrame decodes and displays a Radarcape 0x34 status frame.
func printStatusFrame(num int, payload []byte) {
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	settings := payload[8]
	ppsDelta := int8(payload[9])
	gpsStatus := payload[10]

	gpsMode := "legacy"
	if settings&0x10 != 0 {
		gpsMode = "GPS"
	}

	fmt.Printf("#%-5d STATUS  TS=%-14d  settings=0x%02X(%s)  delta=%d  gps=0x%02X\n",
		num, ts, settings, gpsMode, ppsDelta, gpsStatus)
}

// printPositionFrame decodes and displays a Radarcape 0x35 position frame.
// Lat, lon, and alt are IEEE 754 float32 little-endian values.
func printPositionFrame(num int, payload []byte) {
	lat := math.Float32frombits(
		uint32(payload[5]) | uint32(payload[6])<<8 | uint32(payload[7])<<16 | uint32(payload[8])<<24,
	)
	lon := math.Float32frombits(
		uint32(payload[9]) | uint32(payload[10])<<8 | uint32(payload[11])<<16 | uint32(payload[12])<<24,
	)
	alt := math.Float32frombits(
		uint32(payload[13]) | uint32(payload[14])<<8 | uint32(payload[15])<<16 | uint32(payload[16])<<24,
	)
	fmt.Printf("#%-5d POSITION  lat=%.6f  lon=%.6f  alt=%.1fm\n",
		num, lat, lon, alt)
}

func printSummary(short, long, status, position, errs int, elapsed time.Duration) {
	total := short + long + status + position
	fmt.Printf("\n── Summary (%s) ──\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Total:    %d frames (%d long, %d short, %d status, %d position)\n",
		total, long, short, status, position)
	if errs > 0 {
		fmt.Printf("  Errors:   %d\n", errs)
	}
	if elapsed > 0 {
		rate := float64(total) / elapsed.Seconds()
		fmt.Printf("  Rate:     %.1f frames/sec\n", rate)
	}
}
