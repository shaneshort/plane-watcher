package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

const expectedTicksPerSec = 100_000_000

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43C03000, "AXI register base address")
	samples := flag.Int("samples", 5, "number of PPS intervals to check")
	flag.Parse()

	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "error: /dev/mem requires Linux (running on %s)\n", runtime.GOOS)
		os.Exit(1)
	}

	r, err := regs.NewMemReader(*baseAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer r.Close()

	fmt.Printf("PPS sanity check at 0x%08X (%d intervals)\n", *baseAddr, *samples)
	fmt.Println("Waiting for first PPS edge...")

	// Wait for initial PPS
	prev := regs.ReadPps(r)
	if prev.Count == 0 {
		fmt.Println("WARNING: PPS count is 0 — no PPS signal detected yet")
		fmt.Println("Waiting up to 10s for first pulse...")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			prev = regs.ReadPps(r)
			if prev.Count > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if prev.Count == 0 {
			fmt.Fprintln(os.Stderr, "FAIL: no PPS edges after 10s — check PPS wiring")
			os.Exit(1)
		}
	}
	fmt.Printf("Initial PPS count=%d counter=%d\n\n", prev.Count, prev.CounterAtPps())

	fmt.Println("INTERVAL  PPS_COUNT  DELTA_TICKS    FREQ_MHZ   ERROR_PPM  STATUS")
	fmt.Println("────────────────────────────────────────────────────────────────────")

	pass := true
	for i := 0; i < *samples; i++ {
		// Wait for PPS count to change
		deadline := time.Now().Add(3 * time.Second)
		var cur regs.PpsState
		for {
			cur = regs.ReadPps(r)
			if cur.Count != prev.Count {
				break
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "FAIL: PPS count stuck at %d for 3s\n", prev.Count)
				os.Exit(1)
			}
			time.Sleep(10 * time.Millisecond)
		}

		edges := cur.Count - prev.Count
		deltaTicks := cur.CounterAtPps() - prev.CounterAtPps()
		ticksPerEdge := deltaTicks / uint64(edges)
		freqMHz := float64(ticksPerEdge) / 1e6
		errorPpm := (float64(ticksPerEdge) - float64(expectedTicksPerSec)) / float64(expectedTicksPerSec) * 1e6

		status := "OK"
		if math.Abs(errorPpm) > 100 {
			status = "WARN"
			pass = false
		}
		if edges != 1 {
			status = fmt.Sprintf("WARN (%d edges)", edges)
		}

		fmt.Printf("  %3d     %7d    %11d  %10.6f  %+9.1f  %s\n",
			i+1, cur.Count, ticksPerEdge, freqMHz, errorPpm, status)

		prev = cur
	}

	fmt.Println()
	if pass {
		fmt.Println("PASS: PPS timing looks healthy")
	} else {
		fmt.Println("WARN: some intervals outside ±100 ppm — check clock source")
	}
}
