# plane-feeder Central Daemon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Evolve plane-feeder from a Beast-only forwarder into the central daemon with radio init, aircraft tracking, and a web dashboard.

**Architecture:** Three new packages (`radio`, `tracker`, `web`) are added alongside the existing Beast/CRC/ICAO pipeline. The FIFO poller sends decoded messages to the tracker via a channel. The web server reads tracker state and FPGA debug counters on demand. Radio tuning happens once at startup via IIO sysfs. Before adding new responsibilities, the existing Beast server is hardened against head-of-line blocking from slow clients.

**Tech Stack:** Go 1.25, `net/http`, `go:embed`, `kreklow.us/go/go-adsb` for Mode-S message decoding, IIO sysfs for AD9361 control.

**Critical startup invariant:** The FPGA version register must be validated before enabling the decoder. If `version == 0x00000000 || version == 0xFFFFFFFF`, the daemon must exit fatally. This applies to ALL non-mock code paths and is checked in Task 6.

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Modify | `ps/server/server.go` | Fix HOL blocking: per-client write goroutines with buffered channels |
| Modify | `ps/server/server_test.go` | Test slow-client isolation |
| Create | `ps/radio/radio.go` | AD9361 IIO sysfs discovery, tune (incl. ENSM FDD), status reads |
| Create | `ps/radio/radio_test.go` | Tests against a mock sysfs directory |
| Create | `ps/tracker/tracker.go` | Aircraft state table, message consumer goroutine |
| Create | `ps/tracker/tracker_test.go` | Tests for message processing, expiry, snapshot |
| Create | `ps/web/web.go` | HTTP server, JSON endpoints, go:embed static assets |
| Create | `ps/web/web_test.go` | Tests for JSON endpoints |
| Create | `ps/web/static/index.html` | Dashboard HTML + vanilla JS |
| Modify | `ps/cmd/plane-feeder/main.go` | Add flags, startup sequence, wire new subsystems |
| Modify | `ps/go.mod` | Add `kreklow.us/go/go-adsb` dependency |

---

### Task 1: Fix Beast Server Head-of-Line Blocking

**Files:**
- Modify: `ps/server/server.go`
- Modify: `ps/server/server_test.go`

The current `Broadcast()` writes synchronously to every client while holding the server mutex. A single slow or wedged client blocks the FIFO poll loop, stalling Beast forwarding, tracker updates, and stats. Since the daemon is gaining more responsibilities, this must be fixed first.

The fix: each client gets a buffered channel and its own write goroutine. `Broadcast()` does non-blocking sends to each channel. If a client's buffer is full, the frame is dropped for that client (slow consumer). The write goroutine drains the channel and removes the client on write error.

- [ ] **Step 1: Write failing test for slow-client isolation**

The test uses `net.Pipe()` to create a deterministic fake connection that never reads, avoiding any dependence on kernel TCP buffer sizes. We add a `AddConn` test helper to inject the pipe's server-side end directly into the server, bypassing the accept loop.

Add to `ps/server/server_test.go`:

```go
func TestSlowClientDoesNotBlockBroadcast(t *testing.T) {
	srv := New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	// Create a deterministic pipe — the "slow" end is never read.
	// net.Pipe is unbuffered: writes block immediately if the peer isn't reading.
	slowServer, _ := net.Pipe()
	defer slowServer.Close()
	srv.AddConn(slowServer)

	// fastConn reads promptly via a real TCP connection.
	fastConn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer fastConn.Close()
	time.Sleep(10 * time.Millisecond)

	frame := []byte{0x1A, 0x33, 0x01, 0x02, 0x03}

	// Broadcast many frames. With the old synchronous Broadcast, this blocks
	// on the first write to the pipe (net.Pipe is zero-buffered).
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			srv.Broadcast(frame)
		}
		close(done)
	}()

	select {
	case <-done:
		// good — Broadcast didn't block
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked — slow client caused head-of-line blocking")
	}

	// fastConn should have received at least some frames.
	buf := make([]byte, 4096)
	fastConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ := fastConn.Read(buf)
	if n == 0 {
		t.Error("fast client received nothing")
	}
}
```

`AddConn` is a test-only method on `Server` that injects a connection directly (used instead of TCP accept). It will be added in Step 3 as part of the server rewrite.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shanes/plane_watcher/ps && go test ./server/ -run TestSlowClient -v -timeout 10s`
Expected: FAIL — `AddConn` doesn't exist yet, and once added, the old synchronous `Broadcast` will block on the zero-buffered `net.Pipe`.

- [ ] **Step 3: Rewrite server.go with per-client goroutines**

```go
// ps/server/server.go
package server

import (
	"fmt"
	"log"
	"net"
	"sync"
)

const clientBufSize = 256 // frames buffered per client before dropping

type client struct {
	conn net.Conn
	ch   chan []byte
}

// Server is a TCP Beast output server that fans out frames
// to all connected clients.
type Server struct {
	port     int
	listener net.Listener
	mu       sync.Mutex
	clients  map[*client]struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func New(port int) *Server {
	return &Server{
		port:    port,
		clients: make(map[*client]struct{}),
		done:    make(chan struct{}),
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return err
	}
	s.listener = ln
	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.listener.Close()
		s.mu.Lock()
		for c := range s.clients {
			close(c.ch)
		}
		s.clients = make(map[*client]struct{})
		s.mu.Unlock()
	})
}

// AddConn injects a pre-existing connection (for testing with net.Pipe).
func (s *Server) AddConn(conn net.Conn) {
	c := &client{
		conn: conn,
		ch:   make(chan []byte, clientBufSize),
	}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	go s.writeLoop(c)
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Broadcast sends a frame to all connected clients.
// Non-blocking: if a client's buffer is full, the frame is dropped for that client.
func (s *Server) Broadcast(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		select {
		case c.ch <- frame:
		default:
			// slow consumer — drop frame
		}
	}
}

func (s *Server) removeClient(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		c.conn.Close()
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		c := &client{
			conn: conn,
			ch:   make(chan []byte, clientBufSize),
		}
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		log.Printf("client connected: %s (%d total)", conn.RemoteAddr(), s.ClientCount())
		go s.writeLoop(c)
	}
}

func (s *Server) writeLoop(c *client) {
	for frame := range c.ch {
		if _, err := c.conn.Write(frame); err != nil {
			s.removeClient(c)
			return
		}
	}
	// channel closed by Stop()
	c.conn.Close()
}
```

- [ ] **Step 4: Run all server tests**

Run: `cd /home/shanes/plane_watcher/ps && go test ./server/ -v -timeout 10s`
Expected: All PASS including `TestSlowClientDoesNotBlockBroadcast`.

- [ ] **Step 5: Run full test suite**

Run: `cd /home/shanes/plane_watcher/ps && go test ./... -timeout 30s`
Expected: All PASS — existing tests still work with the new server.

- [ ] **Step 6: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/server/
git commit -m "fix(ps): eliminate Beast server head-of-line blocking

Each client now gets a buffered channel and dedicated write goroutine.
Broadcast does non-blocking sends; slow consumers drop frames instead
of stalling the FIFO poll loop."
```

---

### Task 2: Radio Package — Sysfs Discovery and Tuning

**Files:**
- Create: `ps/radio/radio.go`
- Create: `ps/radio/radio_test.go`

This package discovers the AD9361 IIO device via sysfs, writes tuning parameters (including ENSM FDD mode), and reads current radio state. All file I/O goes through an injectable base path so tests can use a temp directory.

- [ ] **Step 1: Write failing tests for device discovery, tune, and status**

```go
// ps/radio/radio_test.go
package radio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupMockSysfs(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	devDir := filepath.Join(base, "iio:device0")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "name"), []byte("ad9361-phy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

func writeMockAttr(t *testing.T, base, attr, value string) {
	t.Helper()
	devDir := filepath.Join(base, "iio:device0")
	if err := os.WriteFile(filepath.Join(devDir, attr), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindDevice(t *testing.T) {
	base := setupMockSysfs(t)
	dev, err := findDevice(base)
	if err != nil {
		t.Fatalf("findDevice: %v", err)
	}
	want := filepath.Join(base, "iio:device0")
	if dev != want {
		t.Errorf("got %q, want %q", dev, want)
	}
}

func TestFindDeviceNotPresent(t *testing.T) {
	base := t.TempDir()
	_, err := findDevice(base)
	if err == nil {
		t.Fatal("expected error when no device present")
	}
}

func TestTune(t *testing.T) {
	base := setupMockSysfs(t)
	for _, attr := range []string{
		"ensm_mode",
		"out_altvoltage0_RX_LO_frequency",
		"in_voltage_rf_bandwidth",
		"in_voltage_sampling_frequency",
		"in_voltage0_gain_control_mode",
		"in_voltage0_hardwaregain",
	} {
		writeMockAttr(t, base, attr, "0")
	}

	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Tune("54"); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if !r.tunedOK {
		t.Error("tunedOK should be true after successful tune")
	}

	// Verify LO was written
	devDir := filepath.Join(base, "iio:device0")
	data, _ := os.ReadFile(filepath.Join(devDir, "out_altvoltage0_RX_LO_frequency"))
	if got := strings.TrimSpace(string(data)); got != "1090000000" {
		t.Errorf("RX_LO = %q, want 1090000000", got)
	}

	// Verify ENSM FDD mode was applied
	data, _ = os.ReadFile(filepath.Join(devDir, "ensm_mode"))
	if got := strings.TrimSpace(string(data)); got != "fdd" {
		t.Errorf("ensm_mode = %q, want fdd", got)
	}
}

func TestReadStatus(t *testing.T) {
	base := setupMockSysfs(t)
	writeMockAttr(t, base, "out_altvoltage0_RX_LO_frequency", "1090000000")
	writeMockAttr(t, base, "in_voltage_rf_bandwidth", "2000000")
	writeMockAttr(t, base, "in_voltage0_hardwaregain", "54.000000 dB")

	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	s := r.ReadStatus()
	if s.RXLO != "1090000000" {
		t.Errorf("RXLO = %q, want 1090000000", s.RXLO)
	}
	if s.GainDB != "54.000000 dB" {
		t.Errorf("GainDB = %q, want '54.000000 dB'", s.GainDB)
	}
	if s.TunedOK {
		t.Error("TunedOK should be false before Tune()")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shanes/plane_watcher/ps && go test ./radio/ -v`
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Write the radio package**

```go
// ps/radio/radio.go
package radio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSysfsBase = "/sys/bus/iio/devices"
	deviceName       = "ad9361-phy"

	rxLOFreq   = "1090000000"
	rxBW       = "2000000"
	sampleRate = "30720000"
)

// Status holds the current radio configuration read from sysfs.
type Status struct {
	RXLO       string `json:"rx_lo"`
	RXBW       string `json:"rx_bw"`
	SampleRate string `json:"sample_rate"`
	GainMode   string `json:"gain_mode"`
	GainDB     string `json:"gain_db"`
	RSSI       string `json:"rssi"`
	RXPort     string `json:"rx_port"`
	TunedOK    bool   `json:"tuned_ok"`
}

// Radio controls the AD9361 via IIO sysfs.
type Radio struct {
	devPath string
	tunedOK bool
}

func findDevice(baseDir string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", fmt.Errorf("scan IIO devices: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		nameFile := filepath.Join(baseDir, e.Name(), "name")
		data, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == deviceName {
			return filepath.Join(baseDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no IIO device with name %q found in %s", deviceName, baseDir)
}

// Open discovers the AD9361 device using the default sysfs path.
func Open() (*Radio, error) {
	return OpenAt(defaultSysfsBase)
}

// OpenAt discovers the AD9361 device under a custom base directory.
func OpenAt(baseDir string) (*Radio, error) {
	devPath, err := findDevice(baseDir)
	if err != nil {
		return nil, err
	}
	return &Radio{devPath: devPath}, nil
}

func (r *Radio) write(attr, value string) error {
	return os.WriteFile(filepath.Join(r.devPath, attr), []byte(value), 0o644)
}

func (r *Radio) read(attrs ...string) string {
	for _, attr := range attrs {
		data, err := os.ReadFile(filepath.Join(r.devPath, attr))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// Tune sets the radio to 1090 MHz ADS-B receive parameters.
// gainDB is the manual gain in dB (e.g. "54").
func (r *Radio) Tune(gainDB string) error {
	settings := []struct{ attr, value string }{
		{"ensm_mode", "fdd"},
		{"out_altvoltage0_RX_LO_frequency", rxLOFreq},
		{"in_voltage_rf_bandwidth", rxBW},
		{"in_voltage_sampling_frequency", sampleRate},
		{"in_voltage0_gain_control_mode", "manual"},
		{"in_voltage0_hardwaregain", gainDB},
	}
	for _, s := range settings {
		if err := r.write(s.attr, s.value); err != nil {
			return fmt.Errorf("tune %s=%s: %w", s.attr, s.value, err)
		}
	}
	r.tunedOK = true
	return nil
}

// ReadStatus returns the current radio configuration.
func (r *Radio) ReadStatus() Status {
	return Status{
		RXLO:       r.read("out_altvoltage0_RX_LO_frequency"),
		RXBW:       r.read("in_voltage_rf_bandwidth", "in_voltage0_rf_bandwidth"),
		SampleRate: r.read("in_voltage_sampling_frequency", "in_voltage0_sampling_frequency"),
		GainMode:   r.read("in_voltage0_gain_control_mode"),
		GainDB:     r.read("in_voltage0_hardwaregain"),
		RSSI:       r.read("in_voltage0_rssi"),
		RXPort:     r.read("in_voltage0_rf_port_select"),
		TunedOK:    r.tunedOK,
	}
}
```

- [ ] **Step 4: Run radio tests**

Run: `cd /home/shanes/plane_watcher/ps && go test ./radio/ -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/radio/
git commit -m "feat(ps): add radio package for AD9361 IIO sysfs tuning

Discovers ad9361-phy via sysfs, tunes to 1090 MHz / 2 MHz BW /
30.72 MSPS / manual gain, enables FDD mode."
```

---

### Task 3: Add go-adsb Dependency

**Files:**
- Modify: `ps/go.mod`

- [ ] **Step 1: Add the dependency**

Run: `cd /home/shanes/plane_watcher/ps && go get kreklow.us/go/go-adsb@latest`

- [ ] **Step 2: Tidy**

Run: `cd /home/shanes/plane_watcher/ps && go mod tidy`

- [ ] **Step 3: Verify existing tests still pass**

Run: `cd /home/shanes/plane_watcher/ps && go test ./...`
Expected: All PASS.

- [ ] **Step 4: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/go.mod ps/go.sum
git commit -m "chore(ps): add go-adsb dependency for Mode-S message decoding"
```

---

### Task 4: Tracker Package — Aircraft State Table

**Files:**
- Create: `ps/tracker/tracker.go`
- Create: `ps/tracker/tracker_test.go`

The tracker maintains a `map[uint32]*Aircraft` updated by a goroutine consuming `regs.Message` values from a channel. It uses `go-adsb` to decode callsign, altitude, squawk, and CPR position. The HTTP server reads the map via `Snapshot()`.

- [ ] **Step 1: Write failing tests**

```go
// ps/tracker/tracker_test.go
package tracker

import (
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/regs"
)

func makeDF17() regs.Message {
	var m regs.Message
	m.Len = 14
	m.RPL = 0x100000
	// DF17 = 0x8D (17<<3 | 5)
	m.Bytes[0] = 0x8D
	m.Bytes[1] = 0x75
	m.Bytes[2] = 0x80
	m.Bytes[3] = 0x4B
	return m
}

// runAndWait sends messages, closes the channel, and waits for Run to return.
// This provides deterministic synchronization without time.Sleep.
func runAndWait(tr *Tracker, msgs ...regs.Message) {
	ch := make(chan regs.Message, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	tr.Run(ch) // blocks until channel is drained and closed
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

	// Force expiry by backdating Seen
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shanes/plane_watcher/ps && go test ./tracker/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the tracker package**

```go
// ps/tracker/tracker.go
package tracker

import (
	"fmt"
	"sync"
	"time"

	"kreklow.us/go/go-adsb/adsb"

	"github.com/plane-watcher/plane-feeder/crc"
	"github.com/plane-watcher/plane-feeder/regs"
)

const expiryTTL = 60 * time.Second

// Aircraft holds the current state for one tracked aircraft.
type Aircraft struct {
	ICAO     uint32    `json:"icao"`
	Callsign string    `json:"callsign,omitempty"`
	Altitude int64     `json:"altitude,omitempty"`
	Lat      float64   `json:"lat,omitempty"`
	Lon      float64   `json:"lon,omitempty"`
	Squawk   string    `json:"squawk,omitempty"`
	Signal   uint8     `json:"signal"`
	Seen     time.Time `json:"seen"`
	Messages uint64    `json:"messages"`
}

// Tracker maintains a table of recently-seen aircraft.
type Tracker struct {
	mu       sync.RWMutex
	aircraft map[uint32]*Aircraft
	refLat   float64
	refLon   float64
}

// New creates a tracker with the given receiver reference position.
func New(lat, lon float64) *Tracker {
	return &Tracker{
		aircraft: make(map[uint32]*Aircraft),
		refLat:   lat,
		refLon:   lon,
	}
}

// Run consumes messages from ch until it is closed.
func (t *Tracker) Run(ch <-chan regs.Message) {
	for msg := range ch {
		t.process(msg)
	}
}

// Snapshot returns a copy of the current aircraft map.
func (t *Tracker) Snapshot() map[uint32]Aircraft {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[uint32]Aircraft, len(t.aircraft))
	for k, v := range t.aircraft {
		out[k] = *v
	}
	return out
}

// Count returns the number of tracked aircraft.
func (t *Tracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.aircraft)
}

// Expire removes aircraft not seen within the TTL.
func (t *Tracker) Expire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for icao, ac := range t.aircraft {
		if now.Sub(ac.Seen) > expiryTTL {
			delete(t.aircraft, icao)
		}
	}
}

func (t *Tracker) process(msg regs.Message) {
	df := msg.DF()

	var icao uint32
	switch df {
	case 11, 17, 18:
		icao = msg.ICAO()
	case 0, 4, 5, 16, 20, 21:
		icao = crc.Checksum(msg.Bytes[:msg.Len], msg.Len*8)
	default:
		return
	}

	signal := uint8(msg.RPL >> 16)
	if signal == 0 && msg.RPL > 0 {
		signal = 1
	}

	t.mu.Lock()
	ac, ok := t.aircraft[icao]
	if !ok {
		ac = &Aircraft{ICAO: icao}
		t.aircraft[icao] = ac
	}
	ac.Seen = time.Now()
	ac.Messages++
	ac.Signal = signal
	t.mu.Unlock()

	// Decode fields via go-adsb (best-effort, ignore decode errors).
	var adsbMsg adsb.Message
	if err := adsbMsg.UnmarshalBinary(msg.Bytes[:msg.Len]); err != nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if call, err := adsbMsg.Call(); err == nil {
		ac.Callsign = call
	}

	if alt, err := adsbMsg.Alt(); err == nil {
		ac.Altitude = alt
	}

	if sqk, err := adsbMsg.Sqk(); err == nil && len(sqk) == 4 {
		ac.Squawk = fmt.Sprintf("%d%d%d%d", sqk[0], sqk[1], sqk[2], sqk[3])
	}

	if cprData, err := adsbMsg.CPR(); err == nil {
		if coord, err := cprData.DecodeLocal([]float64{t.refLat, t.refLon}); err == nil {
			ac.Lat = coord[0]
			ac.Lon = coord[1]
		}
	}
}
```

- [ ] **Step 4: Run tracker tests**

Run: `cd /home/shanes/plane_watcher/ps && go test ./tracker/ -v -race`
Expected: All PASS, no data races.

- [ ] **Step 5: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/tracker/
git commit -m "feat(ps): add tracker package for aircraft state table"
```

---

### Task 5: Web Package — HTTP Server, JSON API, and Dashboard

**Files:**
- Create: `ps/web/web.go`
- Create: `ps/web/web_test.go`
- Create: `ps/web/static/index.html`

The web server exposes JSON endpoints and serves an embedded dashboard. Stats are provided via an interface so tests can mock them. The `/api/stats` response includes a `msg_rate` field (messages per second) as required by the design spec.

- [ ] **Step 1: Write failing tests for stats and aircraft endpoints**

```go
// ps/web/web_test.go
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plane-watcher/plane-feeder/tracker"
)

type mockStats struct{}

func (m *mockStats) Stats(debug bool) StatsData {
	return StatsData{
		Uptime:      60,
		MsgCount:    100,
		MsgRate:     10.5,
		DropCount:   5,
		ICAOCount:   10,
		ClientCount: 2,
		PPSCount:    42,
	}
}

func TestStatsEndpoint(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{})

	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var data StatsData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data.MsgCount != 100 {
		t.Errorf("MsgCount = %d, want 100", data.MsgCount)
	}
	if data.MsgRate != 10.5 {
		t.Errorf("MsgRate = %f, want 10.5", data.MsgRate)
	}
}

func TestAircraftEndpoint(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{})

	req := httptest.NewRequest("GET", "/api/aircraft", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var aircraft []tracker.Aircraft
	if err := json.NewDecoder(w.Body).Decode(&aircraft); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(aircraft) != 0 {
		t.Errorf("got %d aircraft, want 0", len(aircraft))
	}
}

func TestDashboardServed(t *testing.T) {
	tr := tracker.New(-31.94, 115.97)
	srv := New(tr, &mockStats{})

	req := httptest.NewRequest("GET", "/index.html", nil)
	w := httptest.NewRecorder()
	srv.handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Error("no Content-Type header")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shanes/plane_watcher/ps && go test ./web/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the web package**

```go
// ps/web/web.go
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sort"

	"github.com/plane-watcher/plane-feeder/radio"
	"github.com/plane-watcher/plane-feeder/tracker"
)

//go:embed static
var staticFiles embed.FS

// StatsData is the JSON payload for /api/stats.
type StatsData struct {
	Uptime        int64             `json:"uptime_s"`
	MsgCount      uint64            `json:"msg_count"`
	MsgRate       float64           `json:"msg_rate"`
	DropCount     uint64            `json:"drop_count"`
	ICAOCount     int               `json:"icao_count"`
	AircraftCount int               `json:"aircraft_count"`
	ClientCount   int               `json:"client_count"`
	PPSCount      uint32            `json:"pps_count"`
	Overflow      bool              `json:"overflow"`
	Radio         radio.Status      `json:"radio"`
	Debug         map[string]uint32 `json:"debug,omitempty"`
}

// StatsProvider is implemented by main.go to supply live stats.
type StatsProvider interface {
	Stats(debug bool) StatsData
}

// Server is the web dashboard HTTP server.
type Server struct {
	tracker  *tracker.Tracker
	stats    StatsProvider
	listener net.Listener
}

// New creates a web server.
func New(t *tracker.Tracker, sp StatsProvider) *Server {
	return &Server{tracker: t, stats: sp}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/aircraft", s.handleAircraft)
	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServerFS(staticSub))
	return mux
}

// Start begins listening on the given port.
func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	s.listener = ln
	go http.Serve(ln, s.handler())
	return nil
}

// Addr returns the listener address.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop closes the listener.
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	debug := r.URL.Query().Get("debug") == "1"
	data := s.stats.Stats(debug)
	data.AircraftCount = s.tracker.Count()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleAircraft(w http.ResponseWriter, r *http.Request) {
	snap := s.tracker.Snapshot()
	list := make([]tracker.Aircraft, 0, len(snap))
	for _, ac := range snap {
		list = append(list, ac)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ICAO < list[j].ICAO
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}
```

- [ ] **Step 4: Create the dashboard HTML**

Create `ps/web/static/index.html` — single-page dashboard with vanilla JS polling `/api/stats` and `/api/aircraft` every 2 seconds. Stats section shows: messages, msg/s, dropped, aircraft, ICAOs, clients, PPS, uptime, overflow. Radio bar shows: LO, BW, gain, RSSI, tuned status. Aircraft table shows: ICAO, callsign, squawk, altitude, lat, lon, signal, messages, age. "Advanced" toggle reveals FPGA debug counter table.

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>plane-watcher</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace;
         background: #1a1a2e; color: #e0e0e0; padding: 1rem; }
  h1 { color: #0ff; margin-bottom: 0.5rem; font-size: 1.4rem; }
  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
           gap: 0.5rem; margin-bottom: 1rem; }
  .stat { background: #16213e; padding: 0.6rem; border-radius: 4px; }
  .stat .label { font-size: 0.7rem; color: #888; text-transform: uppercase; }
  .stat .value { font-size: 1.2rem; font-weight: bold; color: #0ff; }
  .stat .value.warn { color: #f44; }
  table { width: 100%; border-collapse: collapse; margin-top: 0.5rem; }
  th { background: #16213e; color: #0ff; text-align: left; padding: 0.4rem 0.6rem;
       font-size: 0.75rem; text-transform: uppercase; }
  td { padding: 0.3rem 0.6rem; border-bottom: 1px solid #222; font-size: 0.85rem; }
  tr:hover { background: #16213e; }
  .advanced { display: none; margin-top: 1rem; }
  .advanced.show { display: block; }
  .toggle { background: #16213e; border: 1px solid #333; color: #888; padding: 0.3rem 0.8rem;
            cursor: pointer; border-radius: 3px; font-size: 0.75rem; margin-top: 0.5rem; }
  .toggle:hover { color: #0ff; border-color: #0ff; }
  #error { color: #f44; margin-bottom: 0.5rem; display: none; }
  .radio { font-size: 0.8rem; color: #888; margin-bottom: 0.5rem; }
  .radio span { color: #e0e0e0; }
</style>
</head>
<body>
<h1>plane-watcher</h1>
<div id="error"></div>
<div class="radio" id="radio"></div>
<div class="stats" id="stats"></div>
<h2 style="font-size:1rem; margin-bottom:0.3rem;">Aircraft</h2>
<table>
  <thead>
    <tr>
      <th>ICAO</th><th>Callsign</th><th>Squawk</th><th>Alt (ft)</th>
      <th>Lat</th><th>Lon</th><th>Sig</th><th>Msgs</th><th>Age</th>
    </tr>
  </thead>
  <tbody id="aircraft"></tbody>
</table>
<button class="toggle" onclick="toggleAdvanced()">Advanced</button>
<div class="advanced" id="advanced">
  <h2 style="font-size:1rem; margin:0.5rem 0;">FPGA Debug Counters</h2>
  <table>
    <thead><tr><th>Counter</th><th>Value</th></tr></thead>
    <tbody id="debug"></tbody>
  </table>
</div>
<script>
let showDebug = false;
function toggleAdvanced() {
  showDebug = !showDebug;
  document.getElementById('advanced').classList.toggle('show', showDebug);
  if (showDebug) fetchStats();
}
function fmt(n) { return n != null ? n.toLocaleString() : '-'; }
function age(seen) {
  const s = Math.floor((Date.now() - new Date(seen).getTime()) / 1000);
  return s < 0 ? '0s' : s + 's';
}
async function fetchStats() {
  try {
    const url = showDebug ? '/api/stats?debug=1' : '/api/stats';
    const r = await fetch(url);
    const d = await r.json();
    document.getElementById('error').style.display = 'none';
    document.getElementById('stats').innerHTML = [
      ['Messages', fmt(d.msg_count)],
      ['Msg/s', d.msg_rate != null ? d.msg_rate.toFixed(1) : '-'],
      ['Dropped', fmt(d.drop_count)],
      ['Aircraft', fmt(d.aircraft_count)],
      ['ICAOs', fmt(d.icao_count)],
      ['Clients', fmt(d.client_count)],
      ['PPS', fmt(d.pps_count)],
      ['Uptime', fmt(d.uptime_s) + 's'],
      ['Overflow', d.overflow ? '<span class="value warn">YES</span>' : 'no'],
    ].map(([l,v]) => `<div class="stat"><div class="label">${l}</div><div class="value">${v}</div></div>`).join('');
    const radio = d.radio || {};
    document.getElementById('radio').innerHTML =
      `LO: <span>${radio.rx_lo || '?'}</span> | ` +
      `BW: <span>${radio.rx_bw || '?'}</span> | ` +
      `Gain: <span>${radio.gain_db || '?'}</span> | ` +
      `RSSI: <span>${radio.rssi || '?'}</span> | ` +
      `Tuned: <span>${radio.tuned_ok ? 'OK' : 'FAIL'}</span>`;
    if (d.debug) {
      document.getElementById('debug').innerHTML = Object.entries(d.debug)
        .map(([k,v]) => `<tr><td>${k}</td><td>${fmt(v)}</td></tr>`).join('');
    }
  } catch(e) {
    document.getElementById('error').style.display = 'block';
    document.getElementById('error').textContent = 'Connection lost: ' + e.message;
  }
}
async function fetchAircraft() {
  try {
    const r = await fetch('/api/aircraft');
    const list = await r.json();
    const tb = document.getElementById('aircraft');
    if (!list || list.length === 0) {
      tb.innerHTML = '<tr><td colspan="9" style="text-align:center;color:#666">No aircraft</td></tr>';
      return;
    }
    tb.innerHTML = list.map(a =>
      `<tr>` +
      `<td>${a.icao.toString(16).toUpperCase().padStart(6,'0')}</td>` +
      `<td>${a.callsign || ''}</td>` +
      `<td>${a.squawk || ''}</td>` +
      `<td>${a.altitude || ''}</td>` +
      `<td>${a.lat ? a.lat.toFixed(4) : ''}</td>` +
      `<td>${a.lon ? a.lon.toFixed(4) : ''}</td>` +
      `<td>${a.signal}</td>` +
      `<td>${fmt(a.messages)}</td>` +
      `<td>${age(a.seen)}</td>` +
      `</tr>`
    ).join('');
  } catch(e) { /* handled by stats fetch */ }
}
setInterval(() => { fetchStats(); fetchAircraft(); }, 2000);
fetchStats(); fetchAircraft();
</script>
</body>
</html>
```

- [ ] **Step 5: Run web tests**

Run: `cd /home/shanes/plane_watcher/ps && go test ./web/ -v`
Expected: All PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/web/
git commit -m "feat(ps): add web dashboard with stats and aircraft table"
```

---

### Task 6: Wire Everything Into main.go

**Files:**
- Modify: `ps/cmd/plane-feeder/main.go`

Add new CLI flags, startup sequence (with fatal FPGA version check), and wire the new subsystems. Uses `sync/atomic` for `msgCount` and `dropCount` to avoid data races between the poll loop and the HTTP stats handler.

**Critical:** The FPGA version check (`0x00000000` or `0xFFFFFFFF` → fatal) must happen before enabling the decoder. This is a hard requirement from the design spec.

- [ ] **Step 1: Rewrite main.go**

```go
// ps/cmd/plane-feeder/main.go
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/plane-watcher/plane-feeder/beast"
	"github.com/plane-watcher/plane-feeder/crc"
	"github.com/plane-watcher/plane-feeder/icao"
	"github.com/plane-watcher/plane-feeder/radio"
	"github.com/plane-watcher/plane-feeder/regs"
	"github.com/plane-watcher/plane-feeder/server"
	"github.com/plane-watcher/plane-feeder/tracker"
	"github.com/plane-watcher/plane-feeder/web"
)

type statsSource struct {
	startTime time.Time
	reader    regs.RegisterReader
	filter    *icao.Filter
	beastSrv  *server.Server
	rd        *radio.Radio
	msgCount  *atomic.Uint64
	dropCount *atomic.Uint64
	msgRate   *atomic.Int64 // msgs/sec * 10 (fixed-point, written by poll loop)
}

func (s *statsSource) Stats(debug bool) web.StatsData {
	status := s.reader.Read32(regs.RegStatus)
	pps := regs.ReadPps(s.reader)

	d := web.StatsData{
		Uptime:      int64(time.Since(s.startTime).Seconds()),
		MsgCount:    s.msgCount.Load(),
		MsgRate:     float64(s.msgRate.Load()) / 10.0,
		DropCount:   s.dropCount.Load(),
		ICAOCount:   s.filter.Count(),
		ClientCount: s.beastSrv.ClientCount(),
		PPSCount:    pps.Count,
		Overflow:    status&regs.StatusOverflow != 0,
	}

	if s.rd != nil {
		d.Radio = s.rd.ReadStatus()
	}

	if debug {
		d.Debug = readDebugCounters(s.reader)
	}

	return d
}

func readDebugCounters(r regs.RegisterReader) map[string]uint32 {
	return map[string]uint32{
		"rx_valid_ct":      regs.ReadDbg(r, regs.DbgRxValidCt),
		"smp_valid_ct":     regs.ReadDbg(r, regs.DbgSmpValidCt),
		"raw_power_max":    regs.ReadDbg(r, regs.DbgRawPowerMax),
		"raw_power_thr_ct": regs.ReadDbg(r, regs.DbgRawPowerThrCt),
		"power_max":        regs.ReadDbg(r, regs.DbgPowerMax),
		"edge_thr_ct":      regs.ReadDbg(r, regs.DbgEdgeThrCt),
		"power_thr_ct":     regs.ReadDbg(r, regs.DbgPowerThrCt),
		"edge_shape_ct":    regs.ReadDbg(r, regs.DbgEdgeShapeCt),
		"edge_qual_ct":     regs.ReadDbg(r, regs.DbgEdgeQualCt),
		"edge_ct":          regs.ReadDbg(r, regs.DbgEdgeCt),
		"pre_abs_ct":       regs.ReadDbg(r, regs.DbgPreAbsCt),
		"pre_quiet_ct":     regs.ReadDbg(r, regs.DbgPreQuietCt),
		"pre_snr_ct":       regs.ReadDbg(r, regs.DbgPreSnrCt),
		"pre_holdoff_ct":   regs.ReadDbg(r, regs.DbgPreHoldoffCt),
		"pre_pass_ct":      regs.ReadDbg(r, regs.DbgPrePassCt),
		"pre_det_ct":       regs.ReadDbg(r, regs.DbgPreDetCt),
		"som_ct":           regs.ReadDbg(r, regs.DbgSomCt),
		"pre_nofree_ct":    regs.ReadDbg(r, regs.DbgPreNoFreeCt),
		"pre_busy_drop_ct": regs.ReadDbg(r, regs.DbgPreBusyDropCt),
		"dec_busy_max":     regs.ReadDbg(r, regs.DbgDecBusyMax),
		"smallest_done_ct": regs.ReadDbg(r, regs.DbgSmallestDoneCt),
		"invalid_df_ct":    regs.ReadDbg(r, regs.DbgInvalidDfCt),
		"crc_attempt_ct":   regs.ReadDbg(r, regs.DbgCrcAttemptCt),
		"crc_pass_ct":      regs.ReadDbg(r, regs.DbgCrcPassCt),
		"crc_exhaust_ct":   regs.ReadDbg(r, regs.DbgCrcExhaustCt),
		"df4_ct":           regs.ReadDbg(r, regs.DbgDf4Ct),
		"df5_ct":           regs.ReadDbg(r, regs.DbgDf5Ct),
		"df11_ct":          regs.ReadDbg(r, regs.DbgDf11Ct),
		"df17_ct":          regs.ReadDbg(r, regs.DbgDf17Ct),
		"df18_ct":          regs.ReadDbg(r, regs.DbgDf18Ct),
		"cand_df4_ct":      regs.ReadDbg(r, regs.DbgCandDf4Ct),
		"cand_df5_ct":      regs.ReadDbg(r, regs.DbgCandDf5Ct),
		"cand_df11_ct":     regs.ReadDbg(r, regs.DbgCandDf11Ct),
		"msg_ct":           regs.ReadDbg(r, regs.DbgMsgCt),
		"agg_valid_ct":     regs.ReadDbg(r, regs.DbgAggValidCt),
		"agg_drop_ct":      regs.ReadDbg(r, regs.DbgAggDropCt),
		"fifo_wr_ct":       regs.ReadDbg(r, regs.DbgFifoWrCt),
	}
}

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43D00000, "AXI register base address")
	port := flag.Int("port", 30005, "Beast output TCP port")
	httpPort := flag.Int("http-port", 8080, "Web dashboard port")
	radarcape := flag.Bool("radarcape", false, "Use Radarcape timestamp format (UTC via PPS + NTP)")
	gain := flag.String("gain", "54", "AD9361 RX gain in dB (manual mode)")
	lat := flag.Float64("lat", -31.94, "Receiver latitude for CPR decode")
	lon := flag.Float64("lon", 115.97, "Receiver longitude for CPR decode")
	mock := flag.Bool("mock", false, "Use mock reader with empty FIFO (no hardware)")
	flag.Parse()

	// --- 1. Open registers ---
	var reader regs.RegisterReader
	if *mock {
		log.Printf("using mock reader (empty FIFO, for testing)")
		reader = regs.NewMockReader()
	} else {
		if runtime.GOOS != "linux" {
			log.Fatalf("/dev/mem requires Linux (running on %s); use --mock for testing", runtime.GOOS)
		}
		var err error
		reader, err = regs.NewMemReader(*baseAddr)
		if err != nil {
			log.Fatalf("open registers: %v", err)
		}
		log.Printf("registers mapped at 0x%08X", *baseAddr)
	}
	defer reader.Close()

	// --- 2. Validate FPGA (fatal if dead) ---
	ver := reader.Read32(regs.RegVersion)
	if !*mock && (ver == 0x00000000 || ver == 0xFFFFFFFF) {
		log.Fatalf("FPGA not responding (version register = 0x%08X)", ver)
	}
	buildID := ver & 0xFFFF
	log.Printf("hardware version: %d.%d build=0x%04X dirty=%v",
		ver>>16, (ver>>8)&0xFF, buildID, buildID&0x8000 != 0)

	// --- 3. Tune radio (non-fatal on failure) ---
	var rd *radio.Radio
	if !*mock {
		var err error
		rd, err = radio.Open()
		if err != nil {
			log.Printf("WARNING: radio not found: %v", err)
		} else if err := rd.Tune(*gain); err != nil {
			log.Printf("WARNING: radio tune failed: %v", err)
		} else {
			log.Printf("radio tuned: 1090 MHz, gain=%s dB", *gain)
		}
	}

	// --- 4. Enable decoder ---
	reader.Write32(regs.RegControl, regs.ControlEnable)

	// --- 5. Beast TCP server ---
	filter := icao.NewFilter(60 * time.Second)
	beastSrv := server.New(*port)
	if err := beastSrv.Start(); err != nil {
		log.Fatalf("beast server: %v", err)
	}
	log.Printf("Beast output on %s (radarcape=%v)", beastSrv.Addr(), *radarcape)

	// --- 6. Tracker ---
	trk := tracker.New(*lat, *lon)
	trackCh := make(chan regs.Message, 256)
	go trk.Run(trackCh)

	// --- 7. Web server ---
	var msgCount, dropCount atomic.Uint64
	var msgRate atomic.Int64
	ss := &statsSource{
		startTime: time.Now(),
		reader:    reader,
		filter:    filter,
		beastSrv:  beastSrv,
		rd:        rd,
		msgCount:  &msgCount,
		dropCount: &dropCount,
		msgRate:   &msgRate,
	}
	webSrv := web.New(trk, ss)
	if err := webSrv.Start(*httpPort); err != nil {
		log.Fatalf("web server: %v", err)
	}
	log.Printf("web dashboard on %s", webSrv.Addr())

	// --- 8. Signal handler ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var (
		lastStats   time.Time
		lastMsgSnap uint64 // for rate calculation
		ppsRef      beast.PpsTimeRef
	)

	// --- 9. FIFO poll loop ---
	log.Printf("polling FIFO...")
	for {
		select {
		case <-sigCh:
			log.Printf("shutting down (%d messages forwarded, %d dropped)",
				msgCount.Load(), dropCount.Load())
			close(trackCh)
			beastSrv.Stop()
			webSrv.Stop()
			return
		default:
		}

		// Stats tick runs regardless of FIFO state so rate drops to zero
		// when traffic stops instead of displaying a stale value.
		if time.Since(lastStats) > 10*time.Second {
			filter.Expire()
			trk.Expire()
			curMsgCt := msgCount.Load()
			elapsed := time.Since(lastStats).Seconds()
			if elapsed > 0 && lastStats != (time.Time{}) {
				r := float64(curMsgCt-lastMsgSnap) / elapsed
				msgRate.Store(int64(r * 10))
			}
			lastMsgSnap = curMsgCt
			ppsSnap := regs.ReadPps(reader)
			statusSnap := reader.Read32(regs.RegStatus)
			log.Printf("stats: %d msgs, %d dropped, %d icaos, %d aircraft, %d clients, PPS=%d, overflow=%v",
				curMsgCt, dropCount.Load(), filter.Count(), trk.Count(),
				beastSrv.ClientCount(), ppsSnap.Count, statusSnap&regs.StatusOverflow != 0)
			lastStats = time.Now()
		}

		status := reader.Read32(regs.RegStatus)
		if status&regs.StatusNotEmpty == 0 {
			time.Sleep(100 * time.Microsecond)
			continue
		}

		pps := regs.ReadPps(reader)
		if pps.Count != ppsRef.Count && pps.Count > 0 {
			ppsRef = beast.PpsTimeRef{
				Count:   pps.Count,
				WallUTC: time.Now(),
			}
		}

		raw := regs.ReadMessage(reader)
		msg := raw.Decode()
		df := msg.DF()

		switch df {
		case 11, 17, 18:
			filter.Add(msg.ICAO())
		}

		switch df {
		case 0, 4, 5, 16, 20, 21:
			addr := crc.Checksum(msg.Bytes[:msg.Len], msg.Len*8)
			if !filter.Test(addr) {
				dropCount.Add(1)
				continue
			}
		}

		frame := beast.Encode(msg, pps, *radarcape, &ppsRef)
		beastSrv.Broadcast(frame)
		msgCount.Add(1)

		// Non-blocking send to tracker
		select {
		case trackCh <- msg:
		default:
		}
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /home/shanes/plane_watcher/ps && go build ./cmd/plane-feeder/`
Expected: Build succeeds.

- [ ] **Step 3: Run all tests with race detector**

Run: `cd /home/shanes/plane_watcher/ps && go test ./... -race -timeout 30s`
Expected: All PASS, no data races.

- [ ] **Step 4: Verify ARM cross-compilation**

Run: `cd /home/shanes/plane_watcher/ps && CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o /dev/null ./cmd/plane-feeder/`
Expected: Build succeeds (static ARM binary).

- [ ] **Step 5: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/cmd/plane-feeder/main.go
git commit -m "feat(ps): wire radio, tracker, and web into plane-feeder daemon

Startup sequence: FPGA version check (fatal if dead), radio tune
(warn on failure), Beast TCP, tracker goroutine, web dashboard.
Uses atomic counters for race-free stats access."
```

---

### Task 7: Integration Smoke Test

**Files:**
- Create: `ps/web/smoke_test.go`

Verify the web server endpoints return valid JSON when backed by a mock stats provider and empty tracker. This test lives in the `web` package so it has access to `mockStats` from the existing test file.

- [ ] **Step 1: Write the smoke test**

```go
// ps/web/smoke_test.go
package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/plane-watcher/plane-feeder/tracker"
)

func TestSmokeTestWebServer(t *testing.T) {
	trk := tracker.New(-31.94, 115.97)
	srv := New(trk, &mockStats{})
	if err := srv.Start(0); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Stats endpoint
	resp, err := http.Get("http://" + srv.Addr() + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stats: status %d", resp.StatusCode)
	}
	var stats StatsData
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("stats decode: %v", err)
	}

	// Aircraft endpoint
	resp2, err := http.Get("http://" + srv.Addr() + "/api/aircraft")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("aircraft: status %d", resp2.StatusCode)
	}
	var aircraft []tracker.Aircraft
	if err := json.NewDecoder(resp2.Body).Decode(&aircraft); err != nil {
		t.Fatalf("aircraft decode: %v", err)
	}

	// Dashboard HTML
	resp3, err := http.Get("http://" + srv.Addr() + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("dashboard: status %d", resp3.StatusCode)
	}
}
```

- [ ] **Step 2: Run all tests**

Run: `cd /home/shanes/plane_watcher/ps && go test ./... -v -race -timeout 30s`
Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
cd /home/shanes/plane_watcher
git add ps/web/smoke_test.go
git commit -m "test(ps): add web server integration smoke test"
```

---

### Task 8: Final Build Verification

- [ ] **Step 1: Full cross-compile build**

```bash
cd /home/shanes/plane_watcher/ps
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o ../bin/plane-feeder ./cmd/plane-feeder/
ls -lh ../bin/plane-feeder
file ../bin/plane-feeder
```

Expected: Static ARM ELF binary, single file, all web assets embedded.

- [ ] **Step 2: Verify no test regressions**

Run: `cd /home/shanes/plane_watcher/ps && go test ./... -race -timeout 30s`
Expected: All PASS.

- [ ] **Step 3: Run go vet**

Run: `cd /home/shanes/plane_watcher/ps && go vet ./...`
Expected: No issues.
