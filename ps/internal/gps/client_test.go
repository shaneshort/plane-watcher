package gps

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeGpsd implements just enough of the gpsd JSON protocol to drive Client
// tests. It runs on its own goroutine and exposes Inject* helpers for the
// test to push messages back to the client.
type fakeGpsd struct {
	t        *testing.T
	conn     net.Conn // server side
	enc      *json.Encoder
	scan     *bufio.Scanner
	commands chan string
	done     chan struct{}
}

func newFakeGpsd(t *testing.T) (*fakeGpsd, *Client) {
	t.Helper()
	server, client := net.Pipe()
	f := &fakeGpsd{
		t:        t,
		conn:     server,
		enc:      json.NewEncoder(server),
		scan:     bufio.NewScanner(server),
		commands: make(chan string, 16),
		done:     make(chan struct{}),
	}
	f.scan.Buffer(make([]byte, 64*1024), 1024*1024)
	go f.serve()

	// Build a Client around the client side of the pipe. We can't use
	// Dial because we have no listener; instead construct a Client by
	// hand the same way Dial does.
	c := &Client{
		conn:         client,
		enc:          json.NewEncoder(client),
		scan:         bufio.NewScanner(client),
		subs:         make(map[string]*DeviceClient),
		devicesReady: make(chan struct{}),
		closed:       make(chan struct{}),
	}
	c.scan.Buffer(make([]byte, 64*1024), 1024*1024)
	go c.readLoop()
	// Mirror what Dial does: send the ?WATCH handshake on a goroutine so
	// the pipe write doesn't deadlock the test setup. The test will pull
	// the handshake out of f.commands via drainWatchHandshake.
	go func() { _ = c.sendCommand(`?WATCH={"enable":true,"json":true,"raw":1};`) }()

	t.Cleanup(func() {
		c.Close()
		f.Close()
	})

	return f, c
}

func (f *fakeGpsd) Close() {
	f.conn.Close()
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

func (f *fakeGpsd) serve() {
	for f.scan.Scan() {
		line := f.scan.Text()
		select {
		case f.commands <- line:
		default:
		}
	}
}

func (f *fakeGpsd) injectDevices(devices ...DeviceInfo) {
	type dev struct {
		Path   string `json:"path"`
		Driver string `json:"driver"`
	}
	type devices_msg struct {
		Class   string `json:"class"`
		Devices []dev  `json:"devices"`
	}
	msg := devices_msg{Class: "DEVICES"}
	for _, d := range devices {
		msg.Devices = append(msg.Devices, dev{Path: d.Path, Driver: d.Driver})
	}
	if err := f.enc.Encode(msg); err != nil {
		f.t.Fatalf("inject DEVICES: %v", err)
	}
}

// injectRaw pushes a RAW message containing one or more concatenated UBX
// frames for the supplied device path.
func (f *fakeGpsd) injectRaw(devicePath string, frames ...Frame) {
	var buf []byte
	for _, fr := range frames {
		buf = append(buf, fr.Encode()...)
	}
	msg := struct {
		Class  string `json:"class"`
		Device string `json:"device"`
		Data   string `json:"data"`
	}{
		Class:  "RAW",
		Device: devicePath,
		Data:   base64.StdEncoding.EncodeToString(buf),
	}
	if err := f.enc.Encode(msg); err != nil {
		f.t.Fatalf("inject RAW: %v", err)
	}
}

// awaitCommand pulls the next command line received from the client.
func (f *fakeGpsd) awaitCommand(timeout time.Duration) (string, error) {
	select {
	case cmd := <-f.commands:
		return cmd, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("timeout waiting for client command")
	}
}

// drainCommand discards the WATCH handshake the Client sends on startup.
func (f *fakeGpsd) drainWatchHandshake(t *testing.T) {
	t.Helper()
	cmd, err := f.awaitCommand(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cmd, "?WATCH=") {
		t.Fatalf("expected WATCH handshake, got %q", cmd)
	}
}

func TestClient_WatchHandshake(t *testing.T) {
	f, c := newFakeGpsd(t)
	_ = c
	cmd, err := f.awaitCommand(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, `"raw":1`) {
		t.Errorf("WATCH did not include raw=1: %s", cmd)
	}
}

func TestClient_DevicesUpdates(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)

	f.injectDevices(
		DeviceInfo{Path: "/dev/ttyPS1", Driver: "u-blox"},
		DeviceInfo{Path: "/dev/ttyOTHER", Driver: "Generic NMEA"},
	)
	// Give the reader a moment to process.
	time.Sleep(50 * time.Millisecond)

	devs := c.Devices()
	if len(devs) != 2 {
		t.Fatalf("Devices() = %d, want 2: %v", len(devs), devs)
	}
	if devs[0].Driver != "u-blox" {
		t.Errorf("first device driver = %q", devs[0].Driver)
	}
}

func TestDeviceClient_PerDeviceDemux(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)

	subTarget := c.Subscribe("/dev/ttyPS1")
	subOther := c.Subscribe("/dev/ttyOTHER")

	// Inject an ACK-ACK that arrives on /dev/ttyOTHER. The waiter on
	// /dev/ttyPS1 must NOT see it.
	wrongAck := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{ClassCFG, IDCfgTMODE3}}
	f.injectRaw("/dev/ttyOTHER", wrongAck)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := subTarget.WaitFor(ctx, func(fr Frame) bool {
		ok, _, _, _ := fr.IsAck()
		return ok
	})
	if err == nil {
		t.Fatal("subTarget should not receive frames for /dev/ttyOTHER")
	}

	// The "other" subscriber must have seen the frame.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	got, err := subOther.WaitFor(ctx2, func(fr Frame) bool { return true })
	if err != nil {
		t.Fatalf("subOther should receive its frame: %v", err)
	}
	ok, _, _, _ := got.IsAck()
	if !ok {
		t.Errorf("expected ack on subOther, got class=%02x id=%02x", got.Class, got.ID)
	}
}

func TestDeviceClient_ClassIDCorrelation(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	// After SendUBX is observed on the fake server, inject an ACK-ACK for
	// the WRONG class/ID first, then the right one. SendAndAwaitACK must
	// skip the wrong one and consume the right one.
	wrongAck := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{ClassMON, IDMonVER}}
	rightAck := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{ClassCFG, IDCfgTMODE3}}
	go func() {
		if _, err := f.awaitCommand(time.Second); err == nil {
			f.injectRaw("/dev/ttyPS1", wrongAck, rightAck)
		}
	}()

	tmode3 := Frame{Class: ClassCFG, ID: IDCfgTMODE3, Payload: BuildSurveyInTMODE3(60, 20000)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := dc.SendAndAwaitACK(ctx, tmode3); err != nil {
		t.Fatalf("SendAndAwaitACK: %v", err)
	}
}

func TestDeviceClient_AckNAK(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	nak := Frame{Class: ClassACK, ID: IDAckNAK, Payload: []byte{ClassCFG, IDCfgTMODE3}}
	go func() {
		if _, err := f.awaitCommand(time.Second); err == nil {
			f.injectRaw("/dev/ttyPS1", nak)
		}
	}()

	tmode3 := Frame{Class: ClassCFG, ID: IDCfgTMODE3, Payload: BuildSurveyInTMODE3(60, 20000)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := dc.SendAndAwaitACK(ctx, tmode3)
	if err == nil || err != ErrAckNAK && !strings.Contains(err.Error(), "ACK-NAK") {
		t.Fatalf("expected ErrAckNAK, got %v", err)
	}
}

// TestDeviceClient_StaleAckDrained verifies the fix for the rev4 review
// finding: a late ACK from a previously timed-out request must NOT satisfy
// a subsequent SendAndAwaitACK on the same class/ID. This is the exact
// failure mode that would let a rollback falsely report success after
// consuming a stale TMODE ACK from the timed-out original write.
func TestDeviceClient_StaleAckDrained(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	// Step 1: send a TMODE3 write that will time out (no reply scripted).
	go func() { _, _ = f.awaitCommand(time.Second) }()
	tmode3 := Frame{Class: ClassCFG, ID: IDCfgTMODE3, Payload: BuildSurveyInTMODE3(60, 20000)}
	ctx1, cancel1 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if err := dc.SendAndAwaitACK(ctx1, tmode3); err == nil {
		t.Fatal("expected timeout on first request")
	}
	cancel1()

	// Step 2: a late ACK arrives now that the first request has given up.
	staleAck := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{ClassCFG, IDCfgTMODE3}}
	f.injectRaw("/dev/ttyPS1", staleAck)
	// Give the reader a beat to buffer it.
	time.Sleep(20 * time.Millisecond)

	// Step 3: simulate the rollback issuing a fresh TMODE3 write. The
	// stale ACK from step 2 must be drained, and the new request must
	// time out (no real ACK is scripted) instead of consuming the stale
	// ACK and falsely reporting success.
	go func() { _, _ = f.awaitCommand(time.Second) }()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := dc.SendAndAwaitACK(ctx2, tmode3); err == nil {
		t.Fatal("rollback request consumed stale ACK from prior timed-out write")
	}
}

func TestDeviceClient_SendUBXFormatsHexdata(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	pollFrame := PollFrame(ClassMON, IDMonVER)
	wantHex := hex.EncodeToString(pollFrame.Encode())

	if err := dc.SendUBX(pollFrame); err != nil {
		t.Fatal(err)
	}
	cmd, err := f.awaitCommand(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, `"path":"/dev/ttyPS1"`) {
		t.Errorf("missing path in command: %s", cmd)
	}
	if !strings.Contains(cmd, fmt.Sprintf(`"hexdata":%q`, wantHex)) {
		t.Errorf("missing/wrong hexdata in command: %s", cmd)
	}
}

func TestClient_RawHexAlternative(t *testing.T) {
	// gpsd may send RAW with "hex" instead of "data" depending on
	// version/config. Verify both work.
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	frame := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{ClassMON, IDMonVER}}
	msg := struct {
		Class  string `json:"class"`
		Device string `json:"device"`
		Hex    string `json:"hex"`
	}{
		Class:  "RAW",
		Device: "/dev/ttyPS1",
		Hex:    hex.EncodeToString(frame.Encode()),
	}
	if err := json.NewEncoder(f.conn).Encode(msg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err := dc.WaitFor(ctx, func(fr Frame) bool {
		ok, _, _, _ := fr.IsAck()
		return ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != ClassACK {
		t.Errorf("got class=%02x, want %02x", got.Class, ClassACK)
	}
}

// silence unused-import warnings if the test set ever shrinks
var _ = io.EOF

// TestClient_BareHexLinesDispatched verifies that gpsd's raw:1 binary
// passthrough format (raw UBX frames emitted as bare hex lines on stdout,
// NOT wrapped in JSON RAW objects) is accepted by the reader and routed
// to the single subscribed device. This is the bug we hit on real
// hardware: gpsd emits "b56201011400..." as a line of its own, and my
// original JSON-only reader silently dropped all of it.
func TestClient_BareHexLinesDispatched(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	// Build two concatenated UBX frames: ACK-ACK for CFG-TMODE3, and a
	// MON-VER response with a small payload.
	ack := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{ClassCFG, IDCfgTMODE3}}
	monver := Frame{Class: ClassMON, ID: IDMonVER, Payload: buildMonVerPayload("EXT CORE 1.00", "00080000")}
	wire := append(ack.Encode(), monver.Encode()...)

	// Write the bare hex (no JSON wrapper) as a line.
	line := hex.EncodeToString(wire) + "\n"
	if _, err := f.conn.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}

	// The subscription must receive the ACK frame first.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err := dc.WaitFor(ctx, func(fr Frame) bool {
		ok, _, _, _ := fr.IsAck()
		return ok
	})
	if err != nil {
		t.Fatalf("expected ACK from bare hex line, got %v", err)
	}
	if got.Class != ClassACK {
		t.Errorf("got class=%02x, want ACK (%02x)", got.Class, ClassACK)
	}

	// And then the MON-VER frame (same line, concatenated).
	got2, err := dc.WaitFor(ctx, func(fr Frame) bool {
		return fr.Class == ClassMON && fr.ID == IDMonVER
	})
	if err != nil {
		t.Fatalf("expected MON-VER from bare hex line, got %v", err)
	}
	if got2.Class != ClassMON || got2.ID != IDMonVER {
		t.Errorf("got class/id=%02x/%02x, want MON-VER", got2.Class, got2.ID)
	}
}

// TestClient_TransportErrorSurfaces verifies that when the reader
// goroutine exits because the connection is closed, a subsequent WaitFor
// call returns a transport-error flavoured error (not the generic "closed"
// one). This is the fix for the review finding that reader shutdown was
// masked as an opaque "subscription closed".
func TestClient_TransportErrorSurfaces(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	// Yank the server-side pipe out from under the client's reader.
	f.conn.Close()

	// A blocking WaitFor should now return an error that contains the
	// transport-error wording, not the generic "subscription closed".
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := dc.WaitFor(ctx, func(Frame) bool { return true })
	if err == nil {
		t.Fatal("expected WaitFor to return an error after reader exit")
	}
	if !strings.Contains(err.Error(), "transport error") && !strings.Contains(err.Error(), "EOF") {
		t.Errorf("expected transport error in %q", err.Error())
	}
}

// TestClient_GpsdErrorWakesWaiterOnLiveSession verifies that when gpsd
// emits an ERROR object over a still-open TCP session, any in-flight
// WaitFor call wakes up immediately with the ERROR message — not a
// generic timeout. Earlier rounds of the code stored transportErr on
// ERROR but left subscriptions open, so callers would time out waiting
// for a response that never came.
func TestClient_GpsdErrorWakesWaiterOnLiveSession(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	// Start a WaitFor in the background that will block until something
	// arrives — a frame, an error, or context timeout. A long context
	// deadline makes it obvious if we're ACCIDENTALLY relying on
	// timeout-driven failure (which would take 2s here instead of ~20ms).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := dc.WaitFor(ctx, func(Frame) bool { return true })
		done <- err
	}()

	// Let the waiter get wired up.
	time.Sleep(20 * time.Millisecond)

	// Now inject a gpsd ERROR — NOT closing the TCP session. The
	// session is still alive from the server side's perspective.
	errMsg := struct {
		Class   string `json:"class"`
		Message string `json:"message"`
	}{Class: "ERROR", Message: "Can't set raw mode on device"}
	if err := json.NewEncoder(f.conn).Encode(errMsg); err != nil {
		t.Fatal(err)
	}

	// The waiter must return promptly (well under the context deadline)
	// and the error must carry the gpsd ERROR text.
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Can't set raw mode") {
			t.Errorf("expected gpsd ERROR message in %q", err.Error())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitFor did not wake on ERROR within 500ms — still relying on timeout")
	}
}
