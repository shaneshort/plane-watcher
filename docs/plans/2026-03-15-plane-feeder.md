# plane-feeder Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Go program that reads decoded ADS-B messages from the FPGA via AXI registers and serves them as Beast binary over TCP.

**Architecture:** Three-layer pipeline — `RegisterReader` interface abstracts hardware access (`/dev/mem` for Zynq, mock for dev), a poll loop reads the FIFO and publishes messages on a channel, a TCP server fans out Beast-encoded frames to all connected clients. `--radarcape` flag selects GPS timestamp encoding vs standard 12 MHz Beast convention.

**Tech Stack:** Go 1.25, no external dependencies (stdlib only: `syscall` for mmap, `net` for TCP, `flag` for CLI, `testing` for tests)

---

## Register Map Reference

From `hdl/rtl/axi_regs.vhd`:

| Offset | Name | Access | Notes |
|--------|------|--------|-------|
| 0x00 | MSG_DATA_0 | R | Message bits [31:0] |
| 0x04 | MSG_DATA_1 | R | Message bits [63:32] |
| 0x08 | MSG_DATA_2 | R | Message bits [95:64] |
| 0x0C | MSG_DATA_3 | R | Message bits [111:96], upper 16 zero |
| 0x10 | TOA_LO | R | Timestamp [31:0] |
| 0x14 | TOA_HI | R | Timestamp [63:32] |
| 0x18 | RPL | R | Signal level [23:0]. **Read pops FIFO** |
| 0x1C | STATUS | R | [0]=not_empty, [1]=full, [2]=overflow, [14:8]=count |
| 0x20 | PPS_COUNT | R | Total PPS edges |
| 0x24 | PPS_CTR_LO | R | Counter at last PPS [31:0] |
| 0x28 | PPS_CTR_HI | R | Counter at last PPS [63:32] |
| 0x2C | CONTROL | RW | [0]=soft_reset (auto-clear), [1]=enable |
| 0x30 | VERSION | R | 0x00010000 |

Read protocol: check STATUS[0], read MSG_DATA_0..3 + TOA_LO/HI, read RPL (pops FIFO).

## Beast Binary Format Reference

```
Frame: 0x1A | type | 6-byte timestamp | 1-byte signal | message_bytes
```

- Type `0x31` = Mode-AC (not used)
- Type `0x32` = Mode-S short (7 message bytes)
- Type `0x33` = Mode-S long (14 message bytes)
- Signal level: 1 byte, 0-255 (scale RPL into this range)
- Timestamp: 6 bytes big-endian
  - Standard Beast: 12 MHz free-running counter. Scale from 16 MHz: `ts12 = toa * 3 / 4`
  - Radarcape: GPS-synchronized. Encode UTC seconds + fractional nanoseconds per Radarcape spec
- Escaping: any `0x1A` byte in the frame body (after the initial `0x1A`) is doubled to `0x1A 0x1A`

---

## Task 1: Project scaffolding

**Files:**
- Create: `ps/go.mod`
- Create: `ps/cmd/plane-feeder/main.go`

**Step 1: Create Go module**

```bash
mkdir -p ps/cmd/plane-feeder
cd ps && go mod init github.com/plane-watcher/plane-feeder
```

**Step 2: Write minimal main**

`ps/cmd/plane-feeder/main.go`:
```go
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43D00000, "AXI register base address")
	port := flag.Int("port", 30005, "Beast output TCP port")
	radarcape := flag.Bool("radarcape", false, "Use Radarcape GPS timestamp format")
	mock := flag.String("mock", "", "Path to mock message file (disables /dev/mem)")
	flag.Parse()

	fmt.Printf("plane-feeder starting\n")
	fmt.Printf("  base-addr: 0x%08X\n", *baseAddr)
	fmt.Printf("  port:      %d\n", *port)
	fmt.Printf("  radarcape: %v\n", *radarcape)
	fmt.Printf("  mock:      %s\n", *mock)

	_ = baseAddr
	_ = port
	_ = radarcape
	_ = mock

	fmt.Fprintln(os.Stderr, "not yet implemented")
	os.Exit(1)
}
```

**Step 3: Build and verify**

```bash
cd ps && go build ./cmd/plane-feeder && ./plane-feeder --help
```

**Step 4: Commit**

```bash
git add ps/
git commit -m "feat(ps): plane-feeder scaffolding with CLI flags"
```

---

## Task 2: RegisterReader interface and types

**Verified byte order (from axi_regs_tb + sim output):**

The FPGA's `bit_flipper` applies `swizzle()` before outputting `msg_bits`, which
reverses the 14 byte order. The AXI registers expose `msg_bits` directly:
- `MSG_DATA_0 [31:0]`  = `msg_bits[31:0]`  = **last** transmitted bytes (CRC tail)
- `MSG_DATA_3 [15:0]`  = `msg_bits[111:96]` = **first** transmitted bytes (DF byte)

Example from sim (test vector `8D75804B580FF2CF7E9BA6`):
```
AXI MSG: word3=0000D001  word2=F7A69B7E  word1=CFF20F58  word0=4B80758D
Concatenated (word3..word0): D001F7A69B7ECFF20F584B80758D
Byte-reversed for Mode-S:    8D75804B580FF2CF7E9BA6F701D0
                              ^^^^^^^^^^^^^^^^^^^^^^^^ message
                                                      ^^^^^^ CRC-24
```

So `Decode()` must: extract 14 bytes from the 4 words in their natural order,
then reverse all 14 bytes to get standard Mode-S byte order (first-transmitted
byte first, as expected by Beast format and pyModeS).

**Files:**
- Create: `ps/regs/regs.go`
- Create: `ps/regs/regs_test.go`

**Step 1: Write the failing test**

`ps/regs/regs_test.go`:
```go
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
	//   FPGA order bytes: 04 95 E1 02 00 A3 65 50 00 00 00 00 00 00
	//   Reversed 14 bytes: 00 00 00 00 00 00 50 65 A3 00 02 E1 95 04
	//   First byte 0x00 → DF = 0 → short (7 bytes)
	raw := RawMessage{
		Data:  [4]uint32{0x0495E102, 0x00A36550, 0x00000000, 0x00000000},
		ToaLo: 1181, ToaHi: 0, RPL: 753992,
	}

	msg := raw.Decode()

	if msg.Len != 7 {
		t.Errorf("Len = %d, want 7 (DF0)", msg.Len)
	}
	// First byte after reversal should have DF=0
	df := msg.Bytes[0] >> 3
	if df != 0 {
		t.Errorf("DF = %d, want 0", df)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd ps && go test ./regs/ -v
```
Expected: FAIL — types not defined.

**Step 3: Write implementation**

`ps/regs/regs.go`:
```go
package regs

// Register offsets from AXI base address (bytes).
const (
	RegMsgData0 = 0x00
	RegMsgData1 = 0x04
	RegMsgData2 = 0x08
	RegMsgData3 = 0x0C
	RegToaLo    = 0x10
	RegToaHi    = 0x14
	RegRPL      = 0x18 // Reading this pops the FIFO
	RegStatus   = 0x1C
	RegPpsCount = 0x20
	RegPpsCtrLo = 0x24
	RegPpsCtrHi = 0x28
	RegControl  = 0x2C
	RegVersion  = 0x30
)

// Status register bit masks.
const (
	StatusNotEmpty = 1 << 0
	StatusFull     = 1 << 1
	StatusOverflow = 1 << 2
)

// RegisterReader abstracts AXI register access.
// MemReader implements this via /dev/mem mmap.
// MockReader implements this for testing.
type RegisterReader interface {
	Read32(offset uint32) uint32
	Write32(offset uint32, value uint32)
	Close() error
}

// RawMessage holds the raw register values for one decoded message.
type RawMessage struct {
	Data  [4]uint32 // MSG_DATA_0..3 as read from AXI
	ToaLo uint32
	ToaHi uint32
	RPL   uint32
}

// Message is a decoded ADS-B/Mode-S message ready for Beast encoding.
type Message struct {
	Bytes [14]byte // Message bytes in standard Mode-S order (first transmitted byte first)
	Len   int      // 7 (short: DF 0-15) or 14 (long: DF 16-31)
	TOA   uint64   // 64-bit timestamp counter value at time of arrival
	RPL   uint32   // Reference power level (signal strength)
}

// PpsState holds the current PPS timing reference.
type PpsState struct {
	Count     uint32 // Total PPS edges seen
	CounterLo uint32 // Counter at last PPS [31:0]
	CounterHi uint32 // Counter at last PPS [63:32]
}

// CounterAtPps returns the full 64-bit counter value at last PPS.
func (p PpsState) CounterAtPps() uint64 {
	return uint64(p.CounterHi)<<32 | uint64(p.CounterLo)
}

// Decode converts raw AXI register values into a Message with
// standard Mode-S byte order (first transmitted byte at index 0).
//
// The FPGA's bit_flipper applies swizzle() which reverses the 14-byte
// order before storing in msg_bits. The AXI registers expose msg_bits
// directly, so MSG_DATA_0 contains the LAST transmitted bytes and
// MSG_DATA_3 contains the FIRST. We extract all 14 bytes from the
// registers in their natural order, then reverse to get Mode-S order.
func (r RawMessage) Decode() Message {
	var m Message
	m.TOA = uint64(r.ToaHi)<<32 | uint64(r.ToaLo)
	m.RPL = r.RPL & 0x00FFFFFF

	// Extract 14 bytes from 4 registers in FPGA order (swizzled).
	// word0 = msg_bits[31:0], word1 = [63:32], word2 = [95:64], word3 = [111:96]
	// Each word is big-endian: bits [31:24] = first byte within that word.
	var fpga [14]byte
	for w := 0; w < 4; w++ {
		base := w * 4
		fpga[base+0] = byte(r.Data[w] >> 24)
		fpga[base+1] = byte(r.Data[w] >> 16)
		fpga[base+2] = byte(r.Data[w] >> 8)
		fpga[base+3] = byte(r.Data[w])
	}
	// word3 only contributes 2 bytes (bits [111:96]), bytes 12-13.
	// Bytes 14-15 from word3 are zero padding — already overwritten above
	// but we only use 14 bytes total.

	// Reverse to get standard Mode-S order (first transmitted = index 0).
	for i := 0; i < 14; i++ {
		m.Bytes[i] = fpga[13-i]
	}

	// Determine length from DF field (top 5 bits of first byte).
	df := m.Bytes[0] >> 3
	if df >= 16 {
		m.Len = 14
	} else {
		m.Len = 7
	}

	return m
}

// ReadMessage reads one message from the FIFO via the register interface.
// Caller must check StatusNotEmpty first.
// The RPL read is last — it pops the FIFO.
func ReadMessage(r RegisterReader) RawMessage {
	var raw RawMessage
	raw.Data[0] = r.Read32(RegMsgData0)
	raw.Data[1] = r.Read32(RegMsgData1)
	raw.Data[2] = r.Read32(RegMsgData2)
	raw.Data[3] = r.Read32(RegMsgData3)
	raw.ToaLo = r.Read32(RegToaLo)
	raw.ToaHi = r.Read32(RegToaHi)
	raw.RPL = r.Read32(RegRPL) // This pops the FIFO
	return raw
}

// ReadPps reads the current PPS state.
func ReadPps(r RegisterReader) PpsState {
	return PpsState{
		Count:     r.Read32(RegPpsCount),
		CounterLo: r.Read32(RegPpsCtrLo),
		CounterHi: r.Read32(RegPpsCtrHi),
	}
}
```

**Step 4: Run test to verify it passes**

```bash
cd ps && go test ./regs/ -v
```

**Step 5: Commit**

```bash
git add ps/regs/
git commit -m "feat(ps): RegisterReader interface and message decoding"
```

---

## Task 3: Beast binary encoder

**Files:**
- Create: `ps/beast/beast.go`
- Create: `ps/beast/beast_test.go`

**Step 1: Write the failing test**

`ps/beast/beast_test.go`:
```go
package beast

import (
	"testing"

	"github.com/plane-watcher/plane-feeder/regs"
)

func TestEncodeLongMessage(t *testing.T) {
	msg := regs.Message{
		Len: 14,
		TOA: 16000000, // 1 second at 16 MHz
		RPL: 50000,
	}
	// Fill with known bytes (no 0x1A to keep it simple)
	for i := range msg.Bytes {
		msg.Bytes[i] = byte(i + 0x80)
	}

	pps := regs.PpsState{} // counter_at_pps = 0

	frame := Encode(msg, pps, false)

	// Check framing
	if frame[0] != 0x1A {
		t.Errorf("expected start byte 0x1A, got 0x%02X", frame[0])
	}
	if frame[1] != TypeLong {
		t.Errorf("expected type 0x33, got 0x%02X", frame[1])
	}
	// 1A + type + 6 ts + 1 signal + 14 msg = 22
	if len(frame) != 22 {
		t.Errorf("expected frame length 22, got %d", len(frame))
	}
}

func TestEncodeShortMessage(t *testing.T) {
	msg := regs.Message{Len: 7, TOA: 0, RPL: 0}
	pps := regs.PpsState{}
	frame := Encode(msg, pps, false)
	// 1A + type + 6 ts + 1 signal + 7 msg = 15
	if len(frame) != 15 {
		t.Errorf("expected frame length 15, got %d", len(frame))
	}
	if frame[1] != TypeShort {
		t.Errorf("expected type 0x32, got 0x%02X", frame[1])
	}
}

func TestEscaping(t *testing.T) {
	msg := regs.Message{Len: 7, TOA: 0, RPL: 0}
	msg.Bytes[0] = 0x1A // This must be escaped
	msg.Bytes[1] = 0x1A // This too
	pps := regs.PpsState{}
	frame := Encode(msg, pps, false)
	// Should be longer due to escaping
	if len(frame) != 15+2 {
		t.Errorf("expected 17 bytes (2 escaped), got %d", len(frame))
	}
}

func TestStandardTimestamp(t *testing.T) {
	// 16 MHz TOA = 16_000_000 (1 second)
	// 12 MHz equivalent = 12_000_000
	msg := regs.Message{Len: 14, TOA: 16_000_000, RPL: 0}
	pps := regs.PpsState{} // counter_at_pps = 0
	frame := Encode(msg, pps, false)

	// Extract 6-byte timestamp (bytes 2-7), big-endian
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	expected := uint64(12_000_000)
	if ts != expected {
		t.Errorf("expected timestamp %d, got %d", expected, ts)
	}
}

func TestRadarcapeTimestamp(t *testing.T) {
	// With radarcape mode, timestamp is raw 16 MHz counter
	// truncated to 48 bits
	msg := regs.Message{Len: 14, TOA: 0x123456789ABC, RPL: 0}
	pps := regs.PpsState{}
	frame := Encode(msg, pps, true)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 0x123456789ABC {
		t.Errorf("expected raw timestamp 0x123456789ABC, got 0x%012X", ts)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd ps && go test ./beast/ -v
```

**Step 3: Write implementation**

`ps/beast/beast.go`:
```go
package beast

import "github.com/plane-watcher/plane-feeder/regs"

const (
	Escape    = 0x1A
	TypeShort = 0x32 // Mode-S short (7 bytes)
	TypeLong  = 0x33 // Mode-S long (14 bytes)
)

// Encode produces a Beast binary frame from a decoded message.
// If radarcape is true, the raw 16 MHz TOA is used as the timestamp.
// Otherwise, the TOA is scaled to the 12 MHz Beast convention.
func Encode(msg regs.Message, pps regs.PpsState, radarcape bool) []byte {
	var ts48 uint64
	if radarcape {
		ts48 = msg.TOA & 0xFFFFFFFFFFFF
	} else {
		// Scale 16 MHz counter to 12 MHz Beast convention: ts12 = toa * 3 / 4
		ts48 = (msg.TOA * 3 / 4) & 0xFFFFFFFFFFFF
	}

	// Signal level: scale 24-bit RPL to 0-255.
	// Use top 8 bits as a simple approximation.
	signal := byte(msg.RPL >> 16)
	if signal == 0 && msg.RPL > 0 {
		signal = 1 // Don't report zero for non-zero RPL
	}

	msgType := TypeLong
	if msg.Len == 7 {
		msgType = TypeShort
	}

	// Build the unescaped payload (everything after the leading 0x1A)
	payload := make([]byte, 0, 1+6+1+msg.Len)
	payload = append(payload, byte(msgType))

	// 6-byte timestamp, big-endian
	for i := 5; i >= 0; i-- {
		payload = append(payload, byte(ts48>>(i*8)))
	}

	payload = append(payload, signal)
	payload = append(payload, msg.Bytes[:msg.Len]...)

	// Build frame with escaping
	frame := make([]byte, 0, 1+len(payload)*2)
	frame = append(frame, Escape) // Leading 0x1A is NOT escaped
	for _, b := range payload {
		frame = append(frame, b)
		if b == Escape {
			frame = append(frame, Escape) // Double any 0x1A in payload
		}
	}

	return frame
}
```

**Step 4: Run test to verify it passes**

```bash
cd ps && go test ./beast/ -v
```

**Step 5: Commit**

```bash
git add ps/beast/
git commit -m "feat(ps): Beast binary encoder with escaping and timestamp modes"
```

---

## Task 4: Mock register reader

**Files:**
- Create: `ps/regs/mock.go`
- Create: `ps/regs/mock_test.go`

**Step 1: Write the failing test**

`ps/regs/mock_test.go`:
```go
package regs

import "testing"

func TestMockReaderBasic(t *testing.T) {
	mock := NewMockReader()

	// Queue a message
	mock.Push(RawMessage{
		Data:  [4]uint32{0x01020304, 0x05060708, 0x090A0B0C, 0x0D0E0000},
		ToaLo: 100, ToaHi: 0, RPL: 5000,
	})

	// STATUS should show not empty
	status := mock.Read32(RegStatus)
	if status&StatusNotEmpty == 0 {
		t.Error("expected not_empty after push")
	}

	// Read the message
	raw := ReadMessage(mock)
	if raw.Data[0] != 0x01020304 {
		t.Errorf("unexpected data[0]: 0x%08X", raw.Data[0])
	}
	if raw.RPL != 5000 {
		t.Errorf("unexpected RPL: %d", raw.RPL)
	}

	// After pop, should be empty
	status = mock.Read32(RegStatus)
	if status&StatusNotEmpty != 0 {
		t.Error("expected empty after pop")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd ps && go test ./regs/ -v -run TestMock
```

**Step 3: Write implementation**

`ps/regs/mock.go`:
```go
package regs

import "sync"

// MockReader implements RegisterReader with an in-memory FIFO
// for development and testing without hardware.
type MockReader struct {
	mu      sync.Mutex
	queue   []RawMessage
	current RawMessage // Front of queue, exposed via reads
	pps     PpsState
	version uint32
}

func NewMockReader() *MockReader {
	return &MockReader{version: 0x00010000}
}

// Push adds a message to the mock FIFO.
func (m *MockReader) Push(raw RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, raw)
	if len(m.queue) == 1 {
		m.current = m.queue[0]
	}
}

// SetPps sets the mock PPS state.
func (m *MockReader) SetPps(pps PpsState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pps = pps
}

func (m *MockReader) Read32(offset uint32) uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch offset {
	case RegMsgData0:
		return m.current.Data[0]
	case RegMsgData1:
		return m.current.Data[1]
	case RegMsgData2:
		return m.current.Data[2]
	case RegMsgData3:
		return m.current.Data[3]
	case RegToaLo:
		return m.current.ToaLo
	case RegToaHi:
		return m.current.ToaHi
	case RegRPL:
		rpl := m.current.RPL
		// Pop: advance to next message
		if len(m.queue) > 0 {
			m.queue = m.queue[1:]
			if len(m.queue) > 0 {
				m.current = m.queue[0]
			} else {
				m.current = RawMessage{}
			}
		}
		return rpl
	case RegStatus:
		var status uint32
		if len(m.queue) > 0 {
			status |= StatusNotEmpty
		}
		status |= uint32(len(m.queue)&0x7F) << 8
		return status
	case RegPpsCount:
		return m.pps.Count
	case RegPpsCtrLo:
		return m.pps.CounterLo
	case RegPpsCtrHi:
		return m.pps.CounterHi
	case RegVersion:
		return m.version
	default:
		return 0
	}
}

func (m *MockReader) Write32(offset uint32, value uint32) {
	// CONTROL register writes are no-ops in mock
}

func (m *MockReader) Close() error {
	return nil
}
```

**Step 4: Run tests**

```bash
cd ps && go test ./regs/ -v
```

**Step 5: Commit**

```bash
git add ps/regs/mock.go ps/regs/mock_test.go
git commit -m "feat(ps): mock register reader for testing without hardware"
```

---

## Task 5: TCP server with fan-out

**Files:**
- Create: `ps/server/server.go`
- Create: `ps/server/server_test.go`

**Step 1: Write the failing test**

`ps/server/server_test.go`:
```go
package server

import (
	"net"
	"testing"
	"time"
)

func TestServerAcceptsClients(t *testing.T) {
	srv := New(0) // port 0 = OS picks a free port
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	addr := srv.Addr()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Give the server a moment to accept
	time.Sleep(10 * time.Millisecond)

	if srv.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", srv.ClientCount())
	}
}

func TestServerBroadcasts(t *testing.T) {
	srv := New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	// Connect two clients
	conn1, _ := net.Dial("tcp", srv.Addr())
	defer conn1.Close()
	conn2, _ := net.Dial("tcp", srv.Addr())
	defer conn2.Close()

	time.Sleep(10 * time.Millisecond)

	// Broadcast a frame
	frame := []byte{0x1A, 0x33, 0x01, 0x02, 0x03}
	srv.Broadcast(frame)

	// Both clients should receive it
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
		t.Fatalf("expected 1 client, got %d", srv.ClientCount())
	}

	conn.Close()
	// Broadcast to trigger cleanup
	srv.Broadcast([]byte{0x1A, 0x33, 0x00})
	time.Sleep(10 * time.Millisecond)

	if srv.ClientCount() != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", srv.ClientCount())
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd ps && go test ./server/ -v
```

**Step 3: Write implementation**

`ps/server/server.go`:
```go
package server

import (
	"log"
	"net"
	"fmt"
	"sync"
)

// Server is a TCP Beast output server that fans out frames
// to all connected clients.
type Server struct {
	port     int
	listener net.Listener
	mu       sync.Mutex
	clients  map[net.Conn]struct{}
	done     chan struct{}
}

func New(port int) *Server {
	return &Server{
		port:    port,
		clients: make(map[net.Conn]struct{}),
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
	close(s.done)
	s.listener.Close()
	s.mu.Lock()
	for c := range s.clients {
		c.Close()
	}
	s.mu.Unlock()
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
// Dead clients are removed silently.
func (s *Server) Broadcast(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		_, err := c.Write(frame)
		if err != nil {
			c.Close()
			delete(s.clients, c)
		}
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
		s.mu.Lock()
		s.clients[conn] = struct{}{}
		s.mu.Unlock()
		log.Printf("client connected: %s (%d total)", conn.RemoteAddr(), s.ClientCount())
	}
}
```

**Step 4: Run tests**

```bash
cd ps && go test ./server/ -v
```

**Step 5: Commit**

```bash
git add ps/server/
git commit -m "feat(ps): TCP Beast server with fan-out broadcast"
```

---

## Task 6: Poll loop and main integration

**Files:**
- Modify: `ps/cmd/plane-feeder/main.go`

**Step 1: Write the poll loop and integrate everything**

`ps/cmd/plane-feeder/main.go`:
```go
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/plane-watcher/plane-feeder/beast"
	"github.com/plane-watcher/plane-feeder/regs"
	"github.com/plane-watcher/plane-feeder/server"
)

func main() {
	baseAddr := flag.Uint64("base-addr", 0x43D00000, "AXI register base address")
	port := flag.Int("port", 30005, "Beast output TCP port")
	radarcape := flag.Bool("radarcape", false, "Use Radarcape GPS timestamp format")
	mock := flag.String("mock", "", "Use mock reader (no hardware)")
	flag.Parse()

	// Open register interface
	var reader regs.RegisterReader
	var err error
	if *mock != "" {
		log.Printf("using mock reader")
		reader = regs.NewMockReader()
	} else {
		log.Fatalf("TODO: /dev/mem reader at 0x%08X not yet implemented", *baseAddr)
	}
	defer reader.Close()

	// Check hardware version
	ver := reader.Read32(regs.RegVersion)
	log.Printf("hardware version: %d.%d.%d", ver>>16, (ver>>8)&0xFF, ver&0xFF)

	// Enable decoders
	reader.Write32(regs.RegControl, 0x02) // bit 1 = enable

	// Start TCP server
	srv := server.New(*port)
	if err = srv.Start(); err != nil {
		log.Fatalf("server start: %v", err)
	}
	log.Printf("Beast output on %s", srv.Addr())

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Poll loop
	var (
		msgCount  uint64
		pps       regs.PpsState
		lastPps   time.Time
		lastStats time.Time
	)

	_ = baseAddr

	log.Printf("polling FIFO...")
	for {
		select {
		case <-sigCh:
			log.Printf("shutting down (%d messages forwarded)", msgCount)
			srv.Stop()
			return
		default:
		}

		// Refresh PPS state periodically (once per second)
		if time.Since(lastPps) > time.Second {
			pps = regs.ReadPps(reader)
			lastPps = time.Now()
		}

		// Check FIFO
		status := reader.Read32(regs.RegStatus)
		if status&regs.StatusNotEmpty == 0 {
			// FIFO empty — brief sleep to avoid busy-spinning
			time.Sleep(100 * time.Microsecond)
			continue
		}

		// Read and forward message
		raw := regs.ReadMessage(reader)
		msg := raw.Decode()
		frame := beast.Encode(msg, pps, *radarcape)
		srv.Broadcast(frame)
		msgCount++

		// Periodic stats
		if time.Since(lastStats) > 10*time.Second {
			log.Printf("stats: %d msgs, %d clients, PPS=%d, FIFO overflow=%v",
				msgCount, srv.ClientCount(), pps.Count, status&regs.StatusOverflow != 0)
			lastStats = time.Now()
		}
	}
}
```

**Step 2: Build and verify**

```bash
cd ps && go build ./cmd/plane-feeder && ./plane-feeder --mock=test --help
```

**Step 3: Commit**

```bash
git add ps/cmd/plane-feeder/main.go
git commit -m "feat(ps): poll loop and main integration"
```

---

## Task 7: /dev/mem register reader (hardware path)

**Files:**
- Create: `ps/regs/mem.go`

**Step 1: Write implementation**

This only compiles/runs on Linux ARM (Zynq). Use build tags.

`ps/regs/mem.go`:
```go
//go:build linux

package regs

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
)

// MemReader accesses FPGA registers via /dev/mem mmap.
// Only works on Linux (Zynq PS).
type MemReader struct {
	file *os.File
	mem  []byte
}

// NewMemReader opens /dev/mem and mmaps the AXI register region.
func NewMemReader(baseAddr uint64) (*MemReader, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/mem: %w (try running as root)", err)
	}

	// mmap must be page-aligned
	pageSize := uint64(syscall.Getpagesize())
	pageBase := baseAddr & ^(pageSize - 1)
	offset := baseAddr - pageBase
	mapSize := offset + 256 // Enough for our register space

	mem, err := syscall.Mmap(int(f.Fd()), int64(pageBase),
		int(mapSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap 0x%08X: %w", baseAddr, err)
	}

	return &MemReader{file: f, mem: mem[offset:]}, nil
}

func (m *MemReader) Read32(offset uint32) uint32 {
	return binary.LittleEndian.Uint32(m.mem[offset : offset+4])
}

func (m *MemReader) Write32(offset uint32, value uint32) {
	binary.LittleEndian.PutUint32(m.mem[offset:offset+4], value)
}

func (m *MemReader) Close() error {
	if m.mem != nil {
		syscall.Munmap(m.mem)
	}
	if m.file != nil {
		return m.file.Close()
	}
	return nil
}
```

**Step 2: Update main.go to use MemReader**

Replace the `log.Fatalf("TODO...")` block in main.go:

```go
	if *mock != "" {
		log.Printf("using mock reader")
		reader = regs.NewMockReader()
	} else {
		reader, err = regs.NewMemReader(*baseAddr)
		if err != nil {
			log.Fatalf("open registers: %v", err)
		}
		log.Printf("registers mapped at 0x%08X", *baseAddr)
	}
```

**Step 3: Verify it builds on Mac** (MemReader excluded by build tag, mock still works)

```bash
cd ps && go build ./cmd/plane-feeder
```

**Step 4: Cross-compile for Zynq**

```bash
cd ps && GOOS=linux GOARCH=arm GOARM=7 go build -o plane-feeder-arm ./cmd/plane-feeder && file plane-feeder-arm
```
Expected: `ELF 32-bit LSB executable, ARM, EABI5`

**Step 5: Commit**

```bash
git add ps/regs/mem.go ps/cmd/plane-feeder/main.go
git commit -m "feat(ps): /dev/mem register reader for Zynq hardware"
```

---

## Task 8: End-to-end integration test

**Files:**
- Create: `ps/integration_test.go`

**Step 1: Write the test**

`ps/integration_test.go`:
```go
package ps_test

import (
	"net"
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/beast"
	"github.com/plane-watcher/plane-feeder/regs"
	"github.com/plane-watcher/plane-feeder/server"
)

func TestEndToEnd(t *testing.T) {
	// Set up mock reader with two messages
	mock := regs.NewMockReader()
	mock.Push(regs.RawMessage{
		Data:  [4]uint32{0xCE624E00, 0xBEE5C1CE, 0x581D92D7, 0x8D7C1ABE},
		ToaLo: 16_000_000, ToaHi: 0, RPL: 0x800000, // strong signal
	})
	mock.Push(regs.RawMessage{
		Data:  [4]uint32{0xA3650000, 0x04956102, 0x00000000, 0x00000000},
		ToaLo: 32_000_000, ToaHi: 0, RPL: 0x100000, // weaker
	})

	// Start server
	srv := server.New(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	// Connect client
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(10 * time.Millisecond)

	// Simulate poll loop: drain FIFO, encode, broadcast
	pps := regs.PpsState{}
	count := 0
	for {
		status := mock.Read32(regs.RegStatus)
		if status&regs.StatusNotEmpty == 0 {
			break
		}
		raw := regs.ReadMessage(mock)
		msg := raw.Decode()
		frame := beast.Encode(msg, pps, false)
		srv.Broadcast(frame)
		count++
	}

	if count != 2 {
		t.Fatalf("expected 2 messages, processed %d", count)
	}

	// Read from client — should get both Beast frames
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// First frame should start with 0x1A 0x33 (long message)
	if buf[0] != 0x1A || buf[1] != beast.TypeLong {
		t.Errorf("frame 1: expected 1A 33, got %02X %02X", buf[0], buf[1])
	}

	// Find the second frame (search for next unescaped 0x1A)
	found := false
	for i := 1; i < n-1; i++ {
		if buf[i] == 0x1A && buf[i+1] != 0x1A {
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
```

**Step 2: Run the test**

```bash
cd ps && go test -v ./...
```

**Step 3: Commit**

```bash
git add ps/integration_test.go
git commit -m "test(ps): end-to-end integration test with mock reader"
```

---

## Summary

| Task | Component | Key Deliverable |
|------|-----------|----------------|
| 1 | Scaffolding | `go.mod`, CLI flags, builds |
| 2 | RegisterReader | Interface, types, `Decode()`, `ReadMessage()` |
| 3 | Beast encoder | `Encode()` with escaping, standard + radarcape timestamps |
| 4 | Mock reader | In-memory FIFO for development/testing |
| 5 | TCP server | Fan-out broadcast, client management |
| 6 | Main integration | Poll loop wiring everything together |
| 7 | /dev/mem reader | Hardware register access, cross-compile |
| 8 | Integration test | End-to-end: mock → encode → TCP → verify |
