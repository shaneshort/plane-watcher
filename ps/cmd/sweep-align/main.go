package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

type counterSnapshot struct {
	preDet  uint32
	som     uint32
	msg     uint32
	df11    uint32
	df17    uint32
	df18    uint32
	invalid uint32
	crcPass uint32
	status  uint32
}

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43C03000, "AXI register base address")
	delayMin := flag.Int("delay-min", 40, "minimum message_delay")
	delayMax := flag.Int("delay-max", 60, "maximum message_delay")
	delayStep := flag.Int("delay-step", 1, "message_delay step")
	tapMin := flag.Int("tap-min", 68, "minimum output_tap")
	tapMax := flag.Int("tap-max", 84, "maximum output_tap")
	tapStep := flag.Int("tap-step", 1, "output_tap step")
	dwell := flag.Duration("dwell", 3*time.Second, "measurement dwell per setting")
	settle := flag.Duration("settle", 250*time.Millisecond, "settle time after reset per setting")
	enable := flag.Bool("enable", true, "set decoder enable while sweeping")
	flag.Parse()

	if *delayMin < 0 || *delayMax > 255 || *delayMin > *delayMax || *delayStep <= 0 {
		fatalf("invalid delay range")
	}
	if *tapMin < 0 || *tapMax > 127 || *tapMin > *tapMax || *tapStep <= 0 {
		fatalf("invalid tap range")
	}

	r, err := regs.NewMemReader(*baseAddr)
	if err != nil {
		fatalf("open registers: %v", err)
	}
	defer r.Close()

	cfg := regs.NewConfigWriter(r)
	control := r.Read32(regs.RegControl)
	if *enable {
		control |= regs.ControlEnable
		r.Write32(regs.RegControl, control)
	}

	fmt.Println("delay,tap,pre_det_delta,som_delta,msg_delta,df11_delta,df17_delta,df18_delta,invalid_df_delta,crc_pass_delta,fifo_fill,overflow")

	for delay := *delayMin; delay <= *delayMax; delay += *delayStep {
		for tap := *tapMin; tap <= *tapMax; tap += *tapStep {
			if err := cfg.SetMessageDelay(uint32(delay)); err != nil {
				fatalf("set message_delay=%d: %v", delay, err)
			}
			if err := cfg.SetOutputTap(uint32(tap)); err != nil {
				fatalf("set output_tap=%d: %v", tap, err)
			}

			pulseSoftReset(r, control)
			time.Sleep(*settle)

			before := readCounters(r)
			time.Sleep(*dwell)
			after := readCounters(r)

			fill := (after.status >> 8) & 0x7F
			overflow := 0
			if after.status&regs.StatusOverflow != 0 {
				overflow = 1
			}

			fmt.Printf("%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n",
				delay,
				tap,
				delta(after.preDet, before.preDet),
				delta(after.som, before.som),
				delta(after.msg, before.msg),
				delta(after.df11, before.df11),
				delta(after.df17, before.df17),
				delta(after.df18, before.df18),
				delta(after.invalid, before.invalid),
				delta(after.crcPass, before.crcPass),
				fill,
				overflow,
			)
		}
	}
}

func pulseSoftReset(r regs.RegisterReader, control uint32) {
	r.Write32(regs.RegControl, control|regs.ControlSoftReset)
	time.Sleep(10 * time.Millisecond)
	r.Write32(regs.RegControl, control)
}

func readCounters(r regs.RegisterReader) counterSnapshot {
	return counterSnapshot{
		preDet:  regs.ReadDbg(r, regs.DbgPreDetCt),
		som:     regs.ReadDbg(r, regs.DbgSomCt),
		msg:     regs.ReadDbg(r, regs.DbgMsgCt),
		df11:    regs.ReadDbg(r, regs.DbgDf11Ct),
		df17:    regs.ReadDbg(r, regs.DbgDf17Ct),
		df18:    regs.ReadDbg(r, regs.DbgDf18Ct),
		invalid: regs.ReadDbg(r, regs.DbgInvalidDfCt),
		crcPass: regs.ReadDbg(r, regs.DbgCrcPassCt),
		status:  r.Read32(regs.RegStatus),
	}
}

func delta(cur, prev uint32) uint32 {
	return cur - prev
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
