package reorder

import (
	"container/heap"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

// Buffer reorders messages by TOA with a bounded hold window.
// Messages are released once they are older than the newest seen TOA by at
// least maxLateness, or earlier if the heap exceeds maxBuffered.
type Buffer struct {
	maxLateness uint64
	maxBuffered int
	newestTOA   uint64
	haveNewest  bool
	items       messageHeap
}

type entry struct {
	msg regs.Message
}

type messageHeap []entry

func (h messageHeap) Len() int            { return len(h) }
func (h messageHeap) Less(i, j int) bool  { return h[i].msg.TOA < h[j].msg.TOA }
func (h messageHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *messageHeap) Push(x interface{}) { *h = append(*h, x.(entry)) }
func (h *messageHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func New(maxLateness time.Duration, fpgaHz uint64, maxBuffered int) *Buffer {
	if fpgaHz == 0 {
		fpgaHz = 100_000_000
	}
	if maxBuffered <= 0 {
		maxBuffered = 256
	}
	return &Buffer{
		maxLateness: uint64(maxLateness.Nanoseconds()) * fpgaHz / 1_000_000_000,
		maxBuffered: maxBuffered,
		items:       make(messageHeap, 0, maxBuffered),
	}
}

func (b *Buffer) Len() int {
	return len(b.items)
}

func (b *Buffer) Push(msg regs.Message) []regs.Message {
	if !b.haveNewest || msg.TOA > b.newestTOA {
		b.newestTOA = msg.TOA
		b.haveNewest = true
	}
	heap.Push(&b.items, entry{msg: msg})
	return b.flushReady()
}

func (b *Buffer) FlushAll() []regs.Message {
	out := make([]regs.Message, 0, len(b.items))
	for len(b.items) > 0 {
		out = append(out, heap.Pop(&b.items).(entry).msg)
	}
	return out
}

func (b *Buffer) flushReady() []regs.Message {
	if len(b.items) == 0 {
		return nil
	}

	out := make([]regs.Message, 0, len(b.items))
	for len(b.items) > 0 {
		minTOA := b.items[0].msg.TOA
		ready := b.haveNewest && minTOA+b.maxLateness <= b.newestTOA
		overflow := len(b.items) > b.maxBuffered
		if !ready && !overflow {
			break
		}
		out = append(out, heap.Pop(&b.items).(entry).msg)
	}
	return out
}
