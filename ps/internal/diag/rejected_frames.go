package diag

import (
	"fmt"
	"sync"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

const DefaultRejectedFrameCapacity = 128

const ReasonICAOFilterMiss = "icao_filter_miss"
const ReasonDegenerateShortFrame = "degenerate_short_frame"

type RejectedFrame struct {
	Sequence    uint64    `json:"sequence"`
	RecordedAt  time.Time `json:"recorded_at"`
	Reason      string    `json:"reason"`
	DF          uint8     `json:"df"`
	Length      int       `json:"length"`
	Payload     string    `json:"payload"`
	DerivedICAO string    `json:"derived_icao,omitempty"`
	Signal      uint8     `json:"signal"`
	RPL         uint32    `json:"rpl"`
	TOA         uint64    `json:"toa"`
}

type RejectedFrameSummary struct {
	Total    uint64            `json:"total"`
	ByDF     map[string]uint64 `json:"by_df"`
	ByReason map[string]uint64 `json:"by_reason"`
	Recent   []RejectedFrame   `json:"recent"`
}

type RejectedFrameBuffer struct {
	mu    sync.Mutex
	buf   []RejectedFrame
	next  int
	size  int
	total uint64
}

func NewRejectedFrameBuffer(capacity int) *RejectedFrameBuffer {
	if capacity <= 0 {
		capacity = DefaultRejectedFrameCapacity
	}
	return &RejectedFrameBuffer{
		buf: make([]RejectedFrame, capacity),
	}
}

func (b *RejectedFrameBuffer) Record(msg regs.Message, reason string, derivedICAO uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.total++

	payload := ""
	if msg.Len > 0 && msg.Len <= len(msg.Bytes) {
		payload = fmt.Sprintf("%X", msg.Bytes[:msg.Len])
	}

	frame := RejectedFrame{
		Sequence:   b.total,
		RecordedAt: time.Now().UTC(),
		Reason:     reason,
		DF:         msg.DF(),
		Length:     msg.Len,
		Payload:    payload,
		Signal:     regs.RplToSignalByte(msg.RPL),
		RPL:        msg.RPL,
		TOA:        msg.TOA,
	}
	if derivedICAO != 0 {
		frame.DerivedICAO = fmt.Sprintf("%06X", derivedICAO)
	}

	b.buf[b.next] = frame
	b.next = (b.next + 1) % len(b.buf)
	if b.size < len(b.buf) {
		b.size++
	}
}

func (b *RejectedFrameBuffer) Snapshot(limit int) RejectedFrameSummary {
	b.mu.Lock()
	defer b.mu.Unlock()

	if limit <= 0 || limit > b.size {
		limit = b.size
	}

	byDF := make(map[string]uint64)
	byReason := make(map[string]uint64)
	for i := 0; i < b.size; i++ {
		idx := (b.next - 1 - i + len(b.buf)) % len(b.buf)
		frame := b.buf[idx]
		byDF[fmt.Sprintf("DF%d", frame.DF)]++
		byReason[frame.Reason]++
	}

	recent := make([]RejectedFrame, 0, limit)
	for i := 0; i < limit; i++ {
		idx := (b.next - 1 - i + len(b.buf)) % len(b.buf)
		recent = append(recent, b.buf[idx])
	}

	return RejectedFrameSummary{
		Total:    b.total,
		ByDF:     byDF,
		ByReason: byReason,
		Recent:   recent,
	}
}
