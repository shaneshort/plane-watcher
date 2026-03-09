package reorder

import (
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func toas(msgs []regs.Message) []uint64 {
	out := make([]uint64, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, msg.TOA)
	}
	return out
}

func TestBufferReordersWithinWindow(t *testing.T) {
	b := New(50*time.Millisecond, 100_000_000, 16)

	if got := b.Push(regs.Message{TOA: 1_000_000}); len(got) != 0 {
		t.Fatalf("first push flushed unexpectedly: %v", toas(got))
	}
	if got := b.Push(regs.Message{TOA: 2_000_000}); len(got) != 0 {
		t.Fatalf("second push flushed unexpectedly: %v", toas(got))
	}
	got := b.Push(regs.Message{TOA: 1_500_000})
	if len(got) != 0 {
		t.Fatalf("out-of-order push flushed unexpectedly: %v", toas(got))
	}

	got = b.Push(regs.Message{TOA: 7_000_000})
	want := []uint64{1_000_000, 1_500_000, 2_000_000}
	if len(got) != len(want) {
		t.Fatalf("flush len=%d want %d (%v)", len(got), len(want), toas(got))
	}
	for i, toa := range want {
		if got[i].TOA != toa {
			t.Fatalf("flush[%d]=%d want %d (%v)", i, got[i].TOA, toa, toas(got))
		}
	}
}

func TestBufferFlushAllReturnsSorted(t *testing.T) {
	b := New(75*time.Millisecond, 100_000_000, 16)
	b.Push(regs.Message{TOA: 30})
	b.Push(regs.Message{TOA: 10})
	b.Push(regs.Message{TOA: 20})

	got := b.FlushAll()
	want := []uint64{10, 20, 30}
	for i, toa := range want {
		if got[i].TOA != toa {
			t.Fatalf("flushAll[%d]=%d want %d", i, got[i].TOA, toa)
		}
	}
}

func TestBufferMaxBufferedForcesProgress(t *testing.T) {
	b := New(10*time.Second, 100_000_000, 2)

	b.Push(regs.Message{TOA: 30})
	b.Push(regs.Message{TOA: 10})
	got := b.Push(regs.Message{TOA: 20})
	if len(got) != 1 || got[0].TOA != 10 {
		t.Fatalf("overflow flush=%v want [10]", toas(got))
	}
}
