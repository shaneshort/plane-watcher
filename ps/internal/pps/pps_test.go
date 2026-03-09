package pps

import (
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func TestUpdateNoPreviousPps(t *testing.T) {
	w := newWatcherState()
	pps := regs.PpsState{Count: 1, CounterLo: 100_000_000, CounterHi: 0}
	wall := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)

	ref := w.update(pps, wall, true)

	if ref.GpsSync {
		t.Error("GpsSync should be false with only 1 PPS edge")
	}
	if ref.MeasuredTicks != 0 {
		t.Error("MeasuredTicks should be 0 with no previous PPS")
	}
	if ref.Count != 1 {
		t.Errorf("Count: got %d, want 1", ref.Count)
	}
}

func TestUpdateNominalPps(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 200_000_000}, wall2, true)

	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000", ref.MeasuredTicks)
	}
	if ref.Carryover != 0 {
		t.Errorf("Carryover: got %d, want 0", ref.Carryover)
	}
	if !ref.GpsSync {
		t.Error("GpsSync should be true")
	}
	if ref.Degraded {
		t.Error("should not be degraded")
	}
}

func TestUpdateDriftingOscillator(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 199_998_500}, wall2, true)

	if ref.MeasuredTicks != 99_998_500 {
		t.Errorf("MeasuredTicks: got %d, want 99998500", ref.MeasuredTicks)
	}
	if ref.Carryover != -1500 {
		t.Errorf("Carryover: got %d, want -1500", ref.Carryover)
	}
	if !ref.GpsSync {
		t.Error("GpsSync should be true (-1500 is within ±10000)")
	}
}

func TestUpdateSkippedEdges(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 4, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 4, CounterLo: 400_000_000}, wall2, true)

	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000 (averaged over 3 intervals)", ref.MeasuredTicks)
	}
	if !ref.Degraded {
		t.Error("should be degraded (skipped edges)")
	}
	if ref.GpsSync {
		t.Error("GpsSync should be false (skipped edges)")
	}
}

func TestUpdateChronyNotSynced(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, false)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 200_000_000}, wall2, false)

	if ref.GpsSync {
		t.Error("GpsSync should be false when chrony is not synced")
	}
	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks should still be computed: got %d", ref.MeasuredTicks)
	}
}

func TestUpdateInsaneCarryover(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 200_015_000}, wall2, true)

	if ref.GpsSync {
		t.Error("GpsSync should be false when |carryover| > 10000")
	}
}

func TestUpdateNoPpsChange(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	ref1 := w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 1, 500_000_000, time.UTC)
	ref2 := w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall2, true)

	if ref2.Count != ref1.Count {
		t.Error("ref should not change when PPS count is unchanged")
	}
}

func TestUpdateCounterWraps64Bit(t *testing.T) {
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 0xFFFFFF00, CounterHi: 2}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	base := uint64(0xFFFFFF00)
	newLo := uint32(base + 100_000_000)
	newHi := uint32(3)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: newLo, CounterHi: newHi}, wall2, true)

	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000", ref.MeasuredTicks)
	}
}

func TestWatcherPublishesRef(t *testing.T) {
	mock := regs.NewMockReader()
	mock.SetPps(regs.PpsState{Count: 0})

	syncer := &mockSyncer{synced: true}
	w := NewWatcher(mock, syncer)
	w.Start()
	defer w.Stop()

	// Simulate first PPS edge.
	mock.SetPps(regs.PpsState{Count: 1, CounterLo: 100_000_000})
	time.Sleep(600 * time.Millisecond) // watcher ticks at 500ms

	ref := w.Ref()
	if ref == nil {
		t.Fatal("expected non-nil ClockRef after first PPS")
	}
	if ref.Count != 1 {
		t.Errorf("Count: got %d, want 1", ref.Count)
	}

	// Simulate second PPS edge.
	mock.SetPps(regs.PpsState{Count: 2, CounterLo: 200_000_000})
	time.Sleep(600 * time.Millisecond)

	ref = w.Ref()
	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000", ref.MeasuredTicks)
	}
	if !ref.GpsSync {
		t.Error("GpsSync should be true")
	}
}

func TestWatcherChronyExpiry(t *testing.T) {
	mock := regs.NewMockReader()
	syncer := &mockSyncer{synced: true}
	w := NewWatcher(mock, syncer)
	w.Start()
	defer w.Stop()

	// Advance PPS normally to reach synced state.
	mock.SetPps(regs.PpsState{Count: 1, CounterLo: 100_000_000})
	time.Sleep(600 * time.Millisecond)
	mock.SetPps(regs.PpsState{Count: 2, CounterLo: 200_000_000})
	time.Sleep(600 * time.Millisecond)
	mock.SetPps(regs.PpsState{Count: 3, CounterLo: 300_000_000})
	time.Sleep(600 * time.Millisecond)

	ref := w.Ref()
	if !ref.GpsSync {
		t.Fatal("expected GpsSync=true before chrony loss")
	}

	// Chrony loses sync. Keep advancing PPS by exactly 1 each second.
	syncer.synced = false

	for i := uint32(4); i <= 12; i++ {
		mock.SetPps(regs.PpsState{Count: i, CounterLo: i * 100_000_000})
		time.Sleep(600 * time.Millisecond)
	}

	ref = w.Ref()
	if ref.Degraded {
		t.Fatal("test bug: PPS should not be degraded (delta==1 throughout)")
	}
	if ref.GpsSync {
		t.Error("GpsSync should be false after chrony expiry (PPS healthy, chrony lost)")
	}
}

type mockSyncer struct {
	synced bool
}

func (m *mockSyncer) IsSynced() bool {
	return m.synced
}
