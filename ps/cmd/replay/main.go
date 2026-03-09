// replay feeds known decoded messages through the Beast/TCP path
// without hardware. Supports two input formats:
//
//	--format ref    *hex; lines (dump1090 / scan_iq.py --ref output)
//	--format sim    GHDL sim log (DECODED MESSAGE lines with TOA/RPL)
//
// Usage:
//
//	go run ./cmd/replay --file capture2_10M_ref.log --port 30005
//	# then: nc localhost 30005 | hexdump -C
//
// Radarcape timestamps require PPS data which replay files don't contain.
// All replayed frames use standard 12 MHz Beast timestamps.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/beast"
	"github.com/plane-watcher/plane-feeder/internal/server"
)

func main() {
	file := flag.String("file", "", "Input file (required)")
	format := flag.String("format", "ref", "Input format: ref (*hex;) or sim (GHDL log)")
	port := flag.Int("port", 30005, "Beast output TCP port")
	rate := flag.Duration("rate", 100*time.Millisecond, "Delay between messages (0 = burst)")
	loop := flag.Bool("loop", false, "Loop input file continuously")
	flag.Parse()

	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: replay --file <input> [--format ref|sim] [--port 30005] [--rate 100ms]")
		os.Exit(1)
	}

	entries, err := loadFile(*file, *format)
	if err != nil {
		log.Fatalf("load %s: %v", *file, err)
	}
	log.Printf("loaded %d messages from %s (%s format)", len(entries), *file, *format)

	if len(entries) == 0 {
		log.Fatal("no messages found")
	}

	srv := server.New(*port)
	if err := srv.Start(); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Printf("Beast output on %s, waiting for clients...", srv.Addr())

	// Wait for at least one client before replaying
	for srv.ClientCount() == 0 {
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("%d client(s) connected, starting replay", srv.ClientCount())

	sent := 0
	for {
		for _, e := range entries {
			frame := beast.EncodeV2(e.msg, nil)
			srv.Broadcast(frame)
			sent++

			if *rate > 0 {
				time.Sleep(*rate)
			}
		}

		log.Printf("replayed %d messages (%d clients)", sent, srv.ClientCount())

		if !*loop {
			break
		}
	}

	// Keep server alive briefly so clients can drain
	time.Sleep(500 * time.Millisecond)
	srv.Stop()
}
