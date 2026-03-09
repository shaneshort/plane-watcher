package server_test

import (
	"net"
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/beast"
	"github.com/plane-watcher/plane-feeder/internal/regs"
	"github.com/plane-watcher/plane-feeder/internal/server"
)

func TestEndToEnd(t *testing.T) {
	mock := regs.NewMockReader()

	// Known sim values from two_sequential.dat AXI TB output
	mock.Push(regs.RawMessage{
		Data:  [4]uint32{0x4B80758D, 0xCFF20F58, 0xF7A69B7E, 0x0000D001},
		ToaLo: 1181, ToaHi: 0, RPL: 0x800000,
	})
	mock.Push(regs.RawMessage{
		Data:  [4]uint32{0x0495E102, 0x00A36550, 0x00000000, 0x00000000},
		ToaLo: 2000, ToaHi: 0, RPL: 0x100000,
	})

	srv := server.New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(10 * time.Millisecond)

	// Drain FIFO, encode, broadcast
	count := 0
	for {
		status := mock.Read32(regs.RegStatus)
		if status&regs.StatusNotEmpty == 0 {
			break
		}
		raw := regs.ReadMessage(mock)
		msg := raw.Decode()
		frame := beast.EncodeData(beast.ModeBeast12MHz, msg, nil)
		srv.Broadcast(frame)
		count++
	}

	if count != 2 {
		t.Fatalf("processed %d messages, want 2", count)
	}

	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	total := 0
	for {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}
	n := total

	if n < 2 || buf[0] != 0x1A || buf[1] != beast.TypeLong {
		t.Errorf("frame 1: got %02X %02X, want 1A 33", buf[0], buf[1])
	}

	found := false
	for i := 1; i < n-1; i++ {
		if buf[i] == 0x1A {
			if buf[i+1] == 0x1A {
				i++
				continue
			}
			if buf[i+1] == beast.TypeShort {
				found = true
			}
			break
		}
	}
	if !found {
		t.Error("did not find second frame (short message)")
	}
}

func TestOnConnectWelcomeFrames(t *testing.T) {
	// Construct a fake status welcome frame: Beast escape (0x1A) + status type
	// (0x34) followed by recognisable payload bytes.
	welcomePayload := []byte{0x1A, 0x34, 0xDE, 0xAD, 0xBE, 0xEF}
	frames := [][]byte{welcomePayload}

	srv := server.New(0)
	srv.SetWelcomeFrames(frames)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read the welcome frame that should be pushed on connect.
	buf := make([]byte, 256)
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("reading welcome frame: %v", err)
	}

	got := buf[:n]
	if len(got) < len(welcomePayload) {
		t.Fatalf("received %d bytes, want at least %d", len(got), len(welcomePayload))
	}
	for i, b := range welcomePayload {
		if got[i] != b {
			t.Errorf("byte %d: got 0x%02X, want 0x%02X", i, got[i], b)
		}
	}
}
