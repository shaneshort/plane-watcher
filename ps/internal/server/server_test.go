package server

import (
	"net"
	"testing"
	"time"
)

func TestServerAcceptsClients(t *testing.T) {
	srv := New(0)
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

	if srv.ClientCount() != 1 {
		t.Errorf("clients: got %d, want 1", srv.ClientCount())
	}
}

func TestServerBroadcasts(t *testing.T) {
	srv := New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	conn1, _ := net.Dial("tcp", srv.Addr())
	defer conn1.Close()
	conn2, _ := net.Dial("tcp", srv.Addr())
	defer conn2.Close()
	time.Sleep(10 * time.Millisecond)

	frame := []byte{0x1A, 0x33, 0x01, 0x02, 0x03}
	srv.Broadcast(frame)

	buf := make([]byte, 64)

	conn1.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n1, err := conn1.Read(buf)
	if err != nil || n1 != len(frame) {
		t.Errorf("client1: read %d bytes, err=%v", n1, err)
	}

	conn2.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n2, err := conn2.Read(buf)
	if err != nil || n2 != len(frame) {
		t.Errorf("client2: read %d bytes, err=%v", n2, err)
	}
}

func TestServerHandlesDisconnect(t *testing.T) {
	srv := New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	conn, _ := net.Dial("tcp", srv.Addr())
	time.Sleep(10 * time.Millisecond)

	if srv.ClientCount() != 1 {
		t.Fatalf("clients: got %d, want 1", srv.ClientCount())
	}

	conn.Close()
	// First write may succeed on a buffered socket; second forces the error.
	srv.Broadcast([]byte{0x1A, 0x33, 0x00})
	time.Sleep(10 * time.Millisecond)
	srv.Broadcast([]byte{0x1A, 0x33, 0x00})
	time.Sleep(10 * time.Millisecond)

	if srv.ClientCount() != 0 {
		t.Errorf("clients after disconnect: got %d, want 0", srv.ClientCount())
	}
}

func TestSlowClientDoesNotBlockBroadcast(t *testing.T) {
	srv := New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	slowServer, _ := net.Pipe()
	defer slowServer.Close()
	srv.AddConn(slowServer)

	fastConn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer fastConn.Close()
	time.Sleep(10 * time.Millisecond)

	frame := []byte{0x1A, 0x33, 0x01, 0x02, 0x03}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			srv.Broadcast(frame)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked — slow client caused head-of-line blocking")
	}

	buf := make([]byte, 4096)
	fastConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ := fastConn.Read(buf)
	if n == 0 {
		t.Error("fast client received nothing")
	}
}
