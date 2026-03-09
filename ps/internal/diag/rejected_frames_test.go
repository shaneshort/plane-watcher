package diag

import (
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func TestRejectedFrameBufferSnapshotNewestFirst(t *testing.T) {
	buf := NewRejectedFrameBuffer(2)

	buf.Record(regs.Message{
		Bytes: [14]byte{0x20, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		Len:   7,
		RPL:   0x00123456,
		TOA:   101,
	}, ReasonICAOFilterMiss, 0xABC001)
	buf.Record(regs.Message{
		Bytes: [14]byte{0x28, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44},
		Len:   7,
		RPL:   0x00123457,
		TOA:   102,
	}, ReasonICAOFilterMiss, 0xABC002)
	buf.Record(regs.Message{
		Bytes: [14]byte{0xA0, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0xAA, 0xBB, 0xCC, 0xDD},
		Len:   14,
		RPL:   0x00123458,
		TOA:   103,
	}, ReasonICAOFilterMiss, 0xABC003)

	got := buf.Snapshot(10)
	if got.Total != 3 {
		t.Fatalf("total = %d, want 3", got.Total)
	}
	if len(got.Recent) != 2 {
		t.Fatalf("recent len = %d, want 2", len(got.Recent))
	}
	if got.Recent[0].DerivedICAO != "ABC003" {
		t.Fatalf("newest derived_icao = %q, want ABC003", got.Recent[0].DerivedICAO)
	}
	if got.Recent[1].DerivedICAO != "ABC002" {
		t.Fatalf("older derived_icao = %q, want ABC002", got.Recent[1].DerivedICAO)
	}
	if got.ByDF["DF20"] != 1 {
		t.Fatalf("DF20 count = %d, want 1", got.ByDF["DF20"])
	}
	if got.ByDF["DF5"] != 1 {
		t.Fatalf("DF5 count = %d, want 1", got.ByDF["DF5"])
	}
}
