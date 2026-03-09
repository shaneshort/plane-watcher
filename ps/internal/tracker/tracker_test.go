package tracker

import (
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func makeDF17() regs.Message {
	var m regs.Message
	m.Len = 14
	m.RPL = 0x100000
	m.Bytes[0] = 0x8D
	m.Bytes[1] = 0x75
	m.Bytes[2] = 0x80
	m.Bytes[3] = 0x4B
	return m
}

// runAndWait sends messages, closes the channel, and waits for Run to return.
func runAndWait(tr *Tracker, msgs ...regs.Message) {
	ch := make(chan regs.Message, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	tr.Run(ch)
}

func TestTrackerCreatesAircraft(t *testing.T) {
	tr := New(-31.94, 115.97)
	runAndWait(tr, makeDF17())

	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d aircraft, want 1", len(snap))
	}
	ac, ok := snap[0x75804B]
	if !ok {
		t.Fatal("ICAO 0x75804B not found")
	}
	if ac.Messages != 1 {
		t.Errorf("Messages = %d, want 1", ac.Messages)
	}
}

func TestTrackerExpiry(t *testing.T) {
	tr := New(-31.94, 115.97)
	runAndWait(tr, makeDF17())

	tr.mu.Lock()
	for _, ac := range tr.aircraft {
		ac.Seen = time.Now().Add(-2 * time.Minute)
	}
	tr.mu.Unlock()

	tr.Expire()
	snap := tr.Snapshot()
	if len(snap) != 0 {
		t.Errorf("got %d aircraft after expiry, want 0", len(snap))
	}
}

func TestTrackerMultipleMessages(t *testing.T) {
	tr := New(-31.94, 115.97)
	runAndWait(tr, makeDF17(), makeDF17(), makeDF17())

	snap := tr.Snapshot()
	if snap[0x75804B].Messages != 3 {
		t.Errorf("Messages = %d, want 3", snap[0x75804B].Messages)
	}
}

func TestTrackerSignalUsesRplMapping(t *testing.T) {
	tr := New(-31.94, 115.97)
	runAndWait(tr, makeDF17())

	snap := tr.Snapshot()
	ac := snap[0x75804B]
	want := regs.RplToSignalByte(0x100000)
	if ac.Signal != want {
		t.Errorf("Signal = 0x%02X, want 0x%02X", ac.Signal, want)
	}
}
