package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43D00000, "AXI register base address")
	interval := flag.Duration("interval", 1*time.Second, "poll interval")
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("Monitoring FIFO at 0x%08X every %v (Ctrl-C to stop)\n", *baseAddr, *interval)
	fmt.Println("TIME       FILL  EMPTY  FULL  OVERFLOW  PPS")
	fmt.Println("─────────────────────────────────────────────")

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Println("\nstopped")
			return
		case <-ticker.C:
		}

		status := r.Read32(regs.RegStatus)
		notEmpty := status&regs.StatusNotEmpty != 0
		full := status&regs.StatusFull != 0
		overflow := status&regs.StatusOverflow != 0
		fillCount := (status >> 8) & 0x7F
		ppsCount := r.Read32(regs.RegPpsCount)

		now := time.Now().Format("15:04:05")
		fmt.Printf("%s  %3d   %5v  %4v  %8v  %d\n",
			now, fillCount, !notEmpty, full, overflow, ppsCount)
	}
}
