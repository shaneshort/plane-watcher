package regs

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestMessageFromRegisters(t *testing.T) {
	// Known sim output for test vector 8D75804B580FF2CF7E9BA6 (+ CRC F701D0):
	//   word3=0x0000D001 word2=0xF7A69B7E word1=0xCFF20F58 word0=0x4B80758D
	raw := RawMessage{
		Data:  [4]uint32{0x4B80758D, 0xCFF20F58, 0xF7A69B7E, 0x0000D001},
		ToaLo: 1181, ToaHi: 0, RPL: 753992,
	}

	msg := raw.Decode()

	got := strings.ToUpper(hex.EncodeToString(msg.Bytes[:msg.Len]))
	want := "8D75804B580FF2CF7E9BA6F701D0"
	if got != want {
		t.Errorf("Decode() = %s, want %s", got, want)
	}
	if msg.Len != 14 {
		t.Errorf("Len = %d, want 14 (DF17)", msg.Len)
	}
	if msg.TOA != 1181 {
		t.Errorf("TOA = %d, want 1181", msg.TOA)
	}
	if msg.RPL != 753992 {
		t.Errorf("RPL = %d, want 753992", msg.RPL)
	}
}

func TestShortMessageFromRegisters(t *testing.T) {
	// Known sim output for DF0 short message (single_short.dat):
	//   AXI hex (word3..word0): 000000000000000000A365500495E102
	//   word0=0x0495E102 word1=0x00A36550 word2=0x00000000 word3=0x00000000
	raw := RawMessage{
		Data:  [4]uint32{0x0495E102, 0x00A36550, 0x00000000, 0x00000000},
		ToaLo: 1181, ToaHi: 0, RPL: 753992,
	}

	msg := raw.Decode()

	if msg.Len != 7 {
		t.Errorf("Len = %d, want 7 (DF0)", msg.Len)
	}
	df := msg.Bytes[0] >> 3
	if df != 0 {
		t.Errorf("DF = %d, want 0", df)
	}
}

func TestRplToSignalByte(t *testing.T) {
	tests := []struct {
		rpl  uint32
		want byte
	}{
		{rpl: 0, want: 0},
		{rpl: 1, want: 1},
		{rpl: 50_000, want: 0x23},
		{rpl: 753_992, want: 0x88},
		{rpl: 0x100000, want: 0xA1},
		{rpl: 0x200000, want: 0xE4},
		{rpl: RplFullScale, want: 0xFF},
		{rpl: 0xFFFFFF, want: 0xFF},
	}

	for _, tc := range tests {
		got := RplToSignalByte(tc.rpl)
		if got != tc.want {
			t.Errorf("RplToSignalByte(%d) = 0x%02X, want 0x%02X", tc.rpl, got, tc.want)
		}
	}
}

func TestReadPpsStableRetries(t *testing.T) {
	// Simulate a PPS edge landing between the first Count read and the
	// counter reads. The mock returns count=5 on first read, then
	// updated counter values, then count=6 on the verification read.
	// ReadPpsStable must retry and return the consistent count=6 snapshot.
	callNum := 0
	reads := []struct {
		offset uint32
		value  uint32
	}{
		// First attempt: count=5, lo=100M, hi=0, recheck count=6 → mismatch, retry
		{RegPpsCount, 5},
		{RegPpsCtrLo, 200_000_000}, // already updated by new edge
		{RegPpsCtrHi, 0},
		{RegPpsCount, 6}, // mismatch → retry
		// Second attempt: count=6, lo=200M, hi=0, recheck count=6 → match
		{RegPpsCount, 6},
		{RegPpsCtrLo, 200_000_000},
		{RegPpsCtrHi, 0},
		{RegPpsCount, 6},
	}

	reader := &sequenceReader{reads: reads, callNum: &callNum, t: t}
	pps := ReadPpsStable(reader)

	if pps.Count != 6 {
		t.Errorf("Count: got %d, want 6", pps.Count)
	}
	if pps.CounterLo != 200_000_000 {
		t.Errorf("CounterLo: got %d, want 200000000", pps.CounterLo)
	}
	if callNum != 8 {
		t.Errorf("expected 8 reads (1 retry), got %d", callNum)
	}
}

func TestReadPpsStableNoRetryNeeded(t *testing.T) {
	m := NewMockReader()
	m.SetPps(PpsState{Count: 10, CounterLo: 500_000_000, CounterHi: 1})
	pps := ReadPpsStable(m)

	if pps.Count != 10 {
		t.Errorf("Count: got %d, want 10", pps.Count)
	}
	if pps.CounterLo != 500_000_000 {
		t.Errorf("CounterLo: got %d, want 500000000", pps.CounterLo)
	}
	if pps.CounterHi != 1 {
		t.Errorf("CounterHi: got %d, want 1", pps.CounterHi)
	}
}

// sequenceReader returns predetermined values for a sequence of Read32 calls,
// validating that the expected register offset is read each time.
type sequenceReader struct {
	reads   []struct {
		offset uint32
		value  uint32
	}
	callNum *int
	t       *testing.T
}

func (s *sequenceReader) Read32(offset uint32) uint32 {
	idx := *s.callNum
	*s.callNum++
	if idx < len(s.reads) {
		if s.reads[idx].offset != offset {
			s.t.Errorf("Read32 call %d: got offset 0x%02X, want 0x%02X", idx, offset, s.reads[idx].offset)
		}
		return s.reads[idx].value
	}
	s.t.Fatalf("Read32 call %d: unexpected (past end of sequence)", idx)
	return 0
}

func (s *sequenceReader) Write32(offset uint32, value uint32) {}
func (s *sequenceReader) Close() error                        { return nil }
