package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43C03000, "AXI register base address")
	reset := flag.Bool("reset", false, "pulse decoder soft reset before reading capture")
	raw := flag.Bool("raw", false, "dump raw ADC capture bank instead of post-LUT decoder-input capture")
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

	if *reset {
		ctl := r.Read32(regs.RegControl)
		r.Write32(regs.RegControl, ctl|regs.ControlSoftReset)
		r.Write32(regs.RegControl, ctl)
	}

	base := uint32(regs.DbgSampleCaptureBase)
	count := uint32(regs.DbgSampleCaptureCount)
	if *raw {
		base = uint32(regs.DbgRawCaptureBase)
		count = uint32(regs.DbgRawCaptureCount)
		fmt.Println("index,raw_hex,adc_code,otr,frozen")
	} else {
		fmt.Println("sample,word0_hex,power,qualify_valid,pre_det,abs_pass,quiet_pass,snr_pass,edge_hit,som_pending,frozen,word1_hex,quiet_a_pass,quiet_b_pass,quiet_c_pass,quiet_d_pass,pulse_sum_shr4,gap_sum_shr4")
	}
	for i := uint32(0); i < count; i++ {
		word := regs.ReadDbg(r, base+i)
		frozen := (word >> 31) & 1
		if *raw {
			otr := (word >> 12) & 1
			code := word & 0xFFF
			fmt.Printf("%d,0x%08X,%d,%d,%d\n", i, word, code, otr, frozen)
			continue
		}
		if i%2 != 0 {
			continue
		}
		word1 := regs.ReadDbg(r, base+i+1)
		power := signExtend24(word0Payload(word))
		fmt.Printf("%d,0x%08X,%d,%d,%d,%d,%d,%d,%d,%d,%d,0x%08X,%d,%d,%d,%d,%d,%d\n",
			i/2,
			word,
			power,
			(word>>30)&1,
			(word>>29)&1,
			(word>>28)&1,
			(word>>27)&1,
			(word>>26)&1,
			(word>>25)&1,
			(word>>24)&1,
			frozen,
			word1,
			(word1>>31)&1,
			(word1>>30)&1,
			(word1>>29)&1,
			(word1>>28)&1,
			(word1>>14)&0x3FFF,
			word1&0x3FFF,
		)
	}
}

func signExtend24(v uint32) int32 {
	if v&0x00800000 != 0 {
		return int32(v | 0xFF000000)
	}
	return int32(v)
}

func word0Payload(v uint32) uint32 {
	return v & 0x00FFFFFF
}
