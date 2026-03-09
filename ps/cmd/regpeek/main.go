package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43C00000, "AXI register base address")
	start := flag.Uint("start", 0x0, "starting byte offset")
	count := flag.Uint("count", 16, "number of 32-bit registers to read")
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

	fmt.Printf("Register dump at base 0x%08X, start 0x%02X, count %d\n", *baseAddr, *start, *count)
	fmt.Println("OFFSET  VALUE")

	for i := uint(0); i < *count; i++ {
		offset := uint32(*start + i*4)
		value := r.Read32(offset)
		fmt.Printf("0x%02X  0x%08X\n", offset, value)
	}
}
