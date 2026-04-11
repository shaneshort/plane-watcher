package gps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// Client speaks gpsd's JSON protocol over a TCP connection. It opens a
// ?WATCH session with raw=1 so unparsed UBX frames are forwarded as RAW
// objects, and exposes a per-device subscription API for callers that need
// to send UBX commands and wait for typed responses.
//
// Client is safe for concurrent SendUBX calls thanks to an internal writer
// mutex; the reader runs on a single goroutine that demultiplexes frames
// into per-device channels.
type Client struct {
	conn    net.Conn
	enc     *json.Encoder
	scan    *bufio.Scanner
	writeMu sync.Mutex

	debug *log.Logger // optional, set via SetDebug

	subsMu sync.Mutex
	subs   map[string]*DeviceClient // path → subscription

	devicesMu    sync.Mutex
	devices      []DeviceInfo
	devicesReady chan struct{} // closed when first DEVICES message arrives
	devicesOnce  sync.Once

	// transportErr holds the first non-nil error the reader goroutine
	// observes (scanner error, EOF, or a gpsd ERROR message). Waiters
	// prefer this over the generic "subscription closed" so transport
	// failures surface with the real cause.
	transportErr atomic.Value // error

	closeOnce sync.Once
	closed    chan struct{}
}

// DeviceInfo summarises a single device entry from gpsd's DEVICES message.
type DeviceInfo struct {
	Path   string
	Driver string
}

// Dial connects to gpsd and starts the reader goroutine. The caller must
// call Subscribe to start receiving traffic for a specific device. Close
// shuts down the client cleanly.
//
// Dial waits for the first DEVICES message from gpsd (so callers can ask
// "what devices are available?" right after) but the wait is bounded by
// the supplied context, not a hardcoded deadline.
func Dial(ctx context.Context, addr string) (*Client, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("gpsd: dial %s: %w", addr, err)
	}
	c := &Client{
		conn:         conn,
		enc:          json.NewEncoder(conn),
		scan:         bufio.NewScanner(conn),
		subs:         make(map[string]*DeviceClient),
		devicesReady: make(chan struct{}),
		closed:       make(chan struct{}),
	}
	// gpsd sends fairly chunky messages; raise the scanner buffer.
	c.scan.Buffer(make([]byte, 64*1024), 1024*1024)

	// Start the reader before sending ?WATCH so we don't miss the
	// VERSION/DEVICES/WATCH replies.
	go c.readLoop()

	// Send ?WATCH with raw=1 so we get unparsed UBX frames.
	if err := c.sendCommand(`?WATCH={"enable":true,"json":true,"raw":1};`); err != nil {
		c.Close()
		return nil, fmt.Errorf("gpsd: send WATCH: %w", err)
	}

	// Wait for the first DEVICES message. Context-bound so callers with
	// short or long deadlines are respected.
	select {
	case <-c.devicesReady:
	case <-ctx.Done():
		c.Close()
		return nil, fmt.Errorf("gpsd: waiting for DEVICES list: %w", ctx.Err())
	case <-c.closed:
		// Reader exited before DEVICES arrived — surface the real cause.
		if v := c.transportErr.Load(); v != nil {
			return nil, fmt.Errorf("gpsd: transport failed before DEVICES: %w", v.(error))
		}
		return nil, errors.New("gpsd: connection closed before DEVICES arrived")
	}

	return c, nil
}

// SetDebug installs an io.Writer for verbose protocol logging. Pass nil to
// disable. The client wraps the writer in a *log.Logger for line discipline.
func (c *Client) SetDebug(w io.Writer) {
	if w == nil {
		c.debug = nil
		return
	}
	c.debug = log.New(w, "", 0)
}

// Devices returns the most recent device list reported by gpsd. The list is
// updated whenever gpsd sends a DEVICES message; callers typically read it
// once after Dial.
func (c *Client) Devices() []DeviceInfo {
	c.devicesMu.Lock()
	defer c.devicesMu.Unlock()
	out := make([]DeviceInfo, len(c.devices))
	copy(out, c.devices)
	return out
}

// Close shuts down the client and its reader goroutine.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.conn.Close()
		close(c.closed)
	})
	return err
}

// TransportErr returns the first non-nil error the reader observed, or nil
// if the transport is still healthy. Waiters and callers use this to
// distinguish "reader closed cleanly" from "reader died with X".
func (c *Client) TransportErr() error {
	if v := c.transportErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// sendCommand writes a raw JSON line to gpsd. Used for ?WATCH and ?DEVICE.
func (c *Client) sendCommand(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.debug != nil {
		c.debug.Printf("[gpsd tx] %s", line)
	}
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, err := io.WriteString(c.conn, line)
	return err
}

// Subscribe returns a per-device handle that filters incoming RAW/ACK frames
// to the supplied device path. Multiple Subscribes for the same path return
// the same handle.
func (c *Client) Subscribe(devicePath string) *DeviceClient {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	if dc, ok := c.subs[devicePath]; ok {
		return dc
	}
	dc := &DeviceClient{
		client:    c,
		path:      devicePath,
		incoming:  make(chan Frame, 16),
		closed:    make(chan struct{}),
	}
	c.subs[devicePath] = dc
	return dc
}

// readLoop runs on its own goroutine. It reads JSON-per-line from gpsd and
// dispatches messages by class. On exit (scanner error, EOF, or a gpsd
// ERROR message judged fatal) it stores the cause in c.transportErr so
// waiters surface the real failure instead of a generic "closed".
func (c *Client) readLoop() {
	defer func() {
		c.subsMu.Lock()
		for _, dc := range c.subs {
			dc.closeOnce.Do(func() { close(dc.closed) })
		}
		c.subsMu.Unlock()
		// Also release any Dial() caller still waiting for DEVICES.
		c.devicesOnce.Do(func() { close(c.devicesReady) })
		c.closeOnce.Do(func() {
			_ = c.conn.Close()
			close(c.closed)
		})
	}()

	for c.scan.Scan() {
		line := c.scan.Bytes()
		if c.debug != nil {
			c.debug.Printf("[gpsd rx] %s", string(line))
		}
		// gpsd's raw:1 mode for binary protocols (UBX) emits raw
		// device frames as BARE HEX LINES on stdout — not wrapped
		// in JSON RAW objects. Detect and dispatch these first;
		// otherwise my JSON decoder chokes on them and the UBX
		// responses we're polling for are silently dropped.
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) >= 4 && trimmed[0] == 'b' && trimmed[1] == '5' && trimmed[2] == '6' && trimmed[3] == '2' {
			c.handleBareHexLine(trimmed)
			continue
		}
		// All gpsd JSON messages have a "class" field. Decode it
		// first to dispatch.
		var head struct {
			Class   string `json:"class"`
			Device  string `json:"device"`
			Path    string `json:"path"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			if c.debug != nil {
				c.debug.Printf("[gpsd] bad json: %v", err)
			}
			continue
		}
		switch head.Class {
		case "DEVICES":
			c.handleDevicesMsg(line)
		case "DEVICE":
			c.handleDeviceMsg(line)
		case "RAW":
			c.handleRawMsg(line, head.Device)
		case "ERROR":
			// gpsd reports ERROR objects when WATCH/?DEVICE
			// commands are rejected or when the underlying
			// device misbehaves. Store the message AND close
			// every active subscription so in-flight WaitFor
			// calls wake up immediately with the real cause
			// instead of timing out. The reader goroutine keeps
			// running (the TCP session may still be alive), but
			// every pending request is aborted — the caller
			// should Close() and reconnect if they want to
			// continue.
			errMsg := fmt.Errorf("gpsd ERROR: %s", head.Message)
			c.transportErr.Store(errMsg)
			c.subsMu.Lock()
			for _, dc := range c.subs {
				dc.closeOnce.Do(func() { close(dc.closed) })
			}
			c.subsMu.Unlock()
			if c.debug != nil {
				c.debug.Printf("[gpsd] ERROR: %s", head.Message)
			}
		case "WATCH":
			// Acknowledges our ?WATCH command. We don't block on
			// it — the DEVICES message (which follows WATCH) is
			// our readiness signal — but log it in --debug so
			// operators can verify raw=1 etc. were accepted.
			if c.debug != nil {
				c.debug.Printf("[gpsd] WATCH: %s", string(line))
			}
		default:
			// TPV, SKY, VERSION, etc. — not used by us.
		}
	}
	if err := c.scan.Err(); err != nil {
		c.transportErr.Store(err)
	} else if c.transportErr.Load() == nil {
		c.transportErr.Store(io.EOF)
	}
	if c.debug != nil {
		c.debug.Printf("[gpsd] reader exit: %v", c.transportErr.Load())
	}
}

func (c *Client) handleDevicesMsg(line []byte) {
	var msg struct {
		Devices []struct {
			Path   string `json:"path"`
			Driver string `json:"driver"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	c.devicesMu.Lock()
	c.devices = c.devices[:0]
	for _, d := range msg.Devices {
		c.devices = append(c.devices, DeviceInfo{Path: d.Path, Driver: d.Driver})
	}
	c.devicesMu.Unlock()
	// Signal Dial() that the first DEVICES list has arrived. Even an
	// empty list counts as "discovery done" — the caller decides what
	// to do with it.
	c.devicesOnce.Do(func() { close(c.devicesReady) })
}

func (c *Client) handleDeviceMsg(line []byte) {
	// A single-device update; merge into the cached list.
	var msg struct {
		Path   string `json:"path"`
		Driver string `json:"driver"`
	}
	if err := json.Unmarshal(line, &msg); err != nil || msg.Path == "" {
		return
	}
	c.devicesMu.Lock()
	defer c.devicesMu.Unlock()
	for i, d := range c.devices {
		if d.Path == msg.Path {
			c.devices[i].Driver = msg.Driver
			return
		}
	}
	c.devices = append(c.devices, DeviceInfo{Path: msg.Path, Driver: msg.Driver})
}

// handleBareHexLine parses a gpsd raw:1 bare-hex line (e.g.
// "b56201011400d86785..."), decodes one-or-more UBX frames from it, and
// dispatches them. gpsd's raw:1 mode emits binary-device traffic this way
// instead of wrapping it in a JSON {"class":"RAW",...} object.
//
// Bare hex lines do not carry a device path. For the typical single-device
// case we dispatch to the one active subscription. For multi-device setups
// we can't safely correlate, so the frame is dropped with a debug log.
func (c *Client) handleBareHexLine(line []byte) {
	raw, err := hex.DecodeString(string(line))
	if err != nil {
		if c.debug != nil {
			c.debug.Printf("[gpsd] bare hex decode: %v", err)
		}
		return
	}

	c.subsMu.Lock()
	var only *DeviceClient
	switch len(c.subs) {
	case 0:
		c.subsMu.Unlock()
		return
	case 1:
		for _, dc := range c.subs {
			only = dc
		}
	default:
		c.subsMu.Unlock()
		if c.debug != nil {
			c.debug.Printf("[gpsd] bare hex with >1 subscriptions — dropping (cannot correlate)")
		}
		return
	}
	devicePath := only.path
	c.subsMu.Unlock()

	// A single hex line can carry multiple concatenated frames; walk it.
	for i := 0; i < len(raw); {
		// Find next sync.
		j := i
		for j+1 < len(raw) && !(raw[j] == SyncByte1 && raw[j+1] == SyncByte2) {
			j++
		}
		if j+1 >= len(raw) {
			break
		}
		f, n, err := Decode(raw[j:])
		if err != nil {
			// Skip this sync and try the next one.
			i = j + 2
			continue
		}
		c.dispatchFrame(devicePath, f)
		i = j + n
	}
}

func (c *Client) handleRawMsg(line []byte, devicePath string) {
	// gpsd RAW message carries the raw device bytes either as base64 in
	// "data" or hex in "hex". We accept either form.
	var msg struct {
		Data string `json:"data"`
		Hex  string `json:"hex"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	var raw []byte
	switch {
	case msg.Data != "":
		b, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			if c.debug != nil {
				c.debug.Printf("[gpsd] RAW base64 decode: %v", err)
			}
			return
		}
		raw = b
	case msg.Hex != "":
		b, err := hex.DecodeString(msg.Hex)
		if err != nil {
			if c.debug != nil {
				c.debug.Printf("[gpsd] RAW hex decode: %v", err)
			}
			return
		}
		raw = b
	default:
		return
	}

	// A single RAW message can hold multiple concatenated UBX frames
	// (gpsd buffers what it read from the serial port). Walk the buffer
	// looking for UBX sync bytes and decode each frame.
	for i := 0; i < len(raw); {
		// Find next sync.
		j := i
		for j+1 < len(raw) && !(raw[j] == SyncByte1 && raw[j+1] == SyncByte2) {
			j++
		}
		if j+1 >= len(raw) {
			break
		}
		f, n, err := Decode(raw[j:])
		if err != nil {
			// Skip this sync and try the next one.
			i = j + 2
			continue
		}
		c.dispatchFrame(devicePath, f)
		i = j + n
	}
}

func (c *Client) dispatchFrame(devicePath string, f Frame) {
	if c.debug != nil {
		c.debug.Printf("[ubx rx] dev=%s class=%02x id=%02x len=%d",
			devicePath, f.Class, f.ID, len(f.Payload))
	}
	c.subsMu.Lock()
	dc := c.subs[devicePath]
	c.subsMu.Unlock()
	if dc == nil {
		// No subscriber for this device — drop. Per design: per-device
		// demux is the whole point. Frames for /dev/ttyOTHER do not
		// satisfy waiters on /dev/ttyPS1.
		return
	}
	select {
	case dc.incoming <- f:
	default:
		// Drop on slow consumer rather than block the reader.
		if c.debug != nil {
			c.debug.Printf("[ubx rx] dev=%s: drop (consumer slow)", devicePath)
		}
	}
}

// DeviceClient is a per-device handle returned by Client.Subscribe. It is
// the only API the survey-in state machine sees: SendUBX writes through to
// the underlying gpsd connection (with the device path attached), and
// WaitFor blocks for a response matching a class/ID predicate.
type DeviceClient struct {
	client    *Client
	path      string
	incoming  chan Frame
	closeOnce sync.Once
	closed    chan struct{}
}

// Path returns the device path this client is bound to.
func (dc *DeviceClient) Path() string { return dc.path }

// SendUBX wraps the frame in a gpsd ?DEVICE command and writes it. The
// underlying client's writer mutex serialises concurrent calls.
func (dc *DeviceClient) SendUBX(f Frame) error {
	wire := f.Encode()
	if wire == nil {
		return fmt.Errorf("ubx: failed to encode frame %02x %02x", f.Class, f.ID)
	}
	if dc.client.debug != nil {
		dc.client.debug.Printf("[ubx tx] dev=%s class=%02x id=%02x len=%d hex=%s",
			dc.path, f.Class, f.ID, len(f.Payload), hex.EncodeToString(wire))
	}
	cmd := fmt.Sprintf(`?DEVICE={"path":%q,"hexdata":%q};`, dc.path, hex.EncodeToString(wire))
	return dc.client.sendCommand(cmd)
}

// Drain non-destructively empties the incoming channel of any frames buffered
// from previous (typically timed-out) requests. Called before each
// SendAndAwait* operation so a late reply to a prior timed-out request
// cannot satisfy the new request — that would let the rollback handler
// falsely report success after consuming a stale ACK.
//
// Drain returns the number of frames discarded; the caller can log this in
// --debug to spot stale traffic.
func (dc *DeviceClient) Drain() int {
	n := 0
	for {
		select {
		case f, ok := <-dc.incoming:
			if !ok {
				return n
			}
			n++
			if dc.client.debug != nil {
				dc.client.debug.Printf("[ubx rx] dev=%s drain stale class=%02x id=%02x",
					dc.path, f.Class, f.ID)
			}
		default:
			return n
		}
	}
}

// WaitFor blocks until a frame arrives matching the predicate, the context
// is cancelled, or the underlying connection closes. Frames not matching the
// predicate are discarded — callers that care about ordering should not race
// multiple WaitFor calls on the same DeviceClient.
//
// If the reader goroutine has exited with a transport error (EOF, gpsd
// ERROR message, scanner failure), WaitFor returns that error wrapped as a
// transport error rather than a generic "subscription closed".
func (dc *DeviceClient) WaitFor(ctx context.Context, match func(Frame) bool) (Frame, error) {
	for {
		select {
		case f, ok := <-dc.incoming:
			if !ok {
				return Frame{}, dc.closedErr()
			}
			if match(f) {
				return f, nil
			}
			// Drop and keep waiting. Logged in --debug.
			if dc.client.debug != nil {
				dc.client.debug.Printf("[ubx rx] dev=%s drop unmatched class=%02x id=%02x",
					dc.path, f.Class, f.ID)
			}
		case <-dc.closed:
			return Frame{}, dc.closedErr()
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		}
	}
}

// closedErr returns the transport error that killed the reader if any,
// else a generic "closed" error. Used so ops can tell "gpsd dropped us"
// from "we shut ourselves down".
func (dc *DeviceClient) closedErr() error {
	if tErr := dc.client.TransportErr(); tErr != nil {
		return fmt.Errorf("gps: transport error: %w", tErr)
	}
	return errors.New("gps: device subscription closed")
}

// SendAndAwaitACK sends f and waits for an ACK-ACK or ACK-NAK that
// references f's class/ID. It returns nil on ACK-ACK, ErrAckNAK on ACK-NAK,
// or a context error on timeout.
//
// Stale frames buffered from previous timed-out requests are drained
// BEFORE the new request is sent, so a late reply to an earlier request
// cannot falsely satisfy this one.
func (dc *DeviceClient) SendAndAwaitACK(ctx context.Context, f Frame) error {
	dc.Drain()
	if err := dc.SendUBX(f); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	resp, err := dc.WaitFor(ctx, func(rx Frame) bool {
		ok, c, id, _ := rx.IsAck()
		return ok && c == f.Class && id == f.ID
	})
	if err != nil {
		return fmt.Errorf("await ack: %w", err)
	}
	_, _, _, isNak := resp.IsAck()
	if isNak {
		return ErrAckNAK
	}
	return nil
}

// SendAndAwaitResponse sends f and waits for a response with the supplied
// class/ID. Useful for polls (MON-VER, NAV-SVIN, etc.) where the receiver
// echoes back a same-class/id frame containing the requested data.
//
// Stale frames buffered from previous timed-out requests are drained BEFORE
// the new request is sent.
func (dc *DeviceClient) SendAndAwaitResponse(ctx context.Context, f Frame, respClass, respID byte) (Frame, error) {
	dc.Drain()
	if err := dc.SendUBX(f); err != nil {
		return Frame{}, fmt.Errorf("send: %w", err)
	}
	return dc.WaitFor(ctx, func(rx Frame) bool {
		return rx.Class == respClass && rx.ID == respID
	})
}

// ErrAckNAK is returned when the receiver replies with ACK-NAK to a
// configuration command.
var ErrAckNAK = errors.New("gps: receiver replied with ACK-NAK")
