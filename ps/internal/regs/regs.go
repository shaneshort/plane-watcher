package regs

import "fmt"

// Register offsets from AXI base address (bytes).
// Only 16 slots (0x00–0x3C) are reliably addressable through the
// Zynq AXI interconnect on this board.
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
	RegDbgIndex = 0x34 // Write debug counter index here
	RegDbgData  = 0x38 // Read selected debug counter value here
	RegConfig   = 0x3C
)

const (
	VersionDeepDebugFlag = uint32(1 << 31)
)

const (
	ControlSoftReset = 1 << 0
	ControlEnable    = 1 << 1
	ControlSnapshot  = 1 << 2
)

const (
	ConfigQuietScoreShiftMask = 0x7
	ConfigSnrRatioShiftMask   = 0x7 << 3
	ConfigHoldoffMask         = 0x3FF << 6
	ConfigHoldoffShift        = 6
	ConfigMessageDelayMask    = 0xFF << 16
	ConfigMessageDelayShift   = 16
	ConfigOutputTapMask       = 0xFF << 24
	ConfigOutputTapShift      = 24
)

// Status register bit masks.
const (
	StatusNotEmpty = 1 << 0
	StatusFull     = 1 << 1
	StatusOverflow = 1 << 2
)

// Debug counter indices (write to RegDbgIndex, read from RegDbgData).
const (
	DbgPowerMax           = 0
	DbgEdgeThrCt          = 1
	DbgPowerThrCt         = 2
	DbgEdgeCt             = 3
	DbgSomCt              = 4
	DbgMsgCt              = 5
	DbgEdgeShapeCt        = 6
	DbgEdgeQualCt         = 7
	DbgPrePassCt          = 8
	DbgPreDetCt           = 9
	DbgAggValidCt         = 10
	DbgFifoWrCt           = 11
	DbgRxValidCt          = 12
	DbgSmpValidCt         = 13
	DbgCoreClkCt          = 14
	DbgCoreInValCt        = 15
	DbgCoreState          = 16
	DbgPreAbsCt           = 17
	DbgPreQuietCt         = 18
	DbgPreSnrCt           = 19
	DbgPreHoldoffCt       = 20
	DbgRxClkCt            = 21
	DbgSmallestDoneCt     = 22
	DbgInvalidDfCt        = 23
	DbgCrcAttemptCt       = 24
	DbgCrcPassCt          = 25
	DbgCrcExhaustCt       = 26
	DbgCrc0W0             = 27
	DbgCrc0W1             = 28
	DbgCrc0W2             = 29
	DbgCrc0W3             = 30
	DbgPreQaFailCt        = 31
	DbgPreQbFailCt        = 32
	DbgPreQcFailCt        = 33
	DbgPreQdFailCt        = 34
	DbgPrePeakAge         = 35
	DbgPreNoFreeCt        = 36
	DbgPreBusyDropCt      = 37
	DbgDecBusyMax         = 38
	DbgAggDropCt          = 39
	DbgDf4Ct              = 40
	DbgDf5Ct              = 41
	DbgDf11Ct             = 42
	DbgDf17Ct             = 43
	DbgDf18Ct             = 44
	DbgCandDf4Ct          = 45
	DbgCandDf5Ct          = 46
	DbgCandDf11Ct         = 47
	DbgRawPowerMax        = 48
	DbgRawPowerThrCt      = 49
	DbgRawIqNearrailCt    = 50
	DbgRawPowerSatCt      = 51
	DbgSampleFifoOvfCt    = 52
	DbgRawIq75PctCt       = 53
	DbgRawIq87P5PctCt     = 54
	DbgAdcCodeMin         = 55
	DbgAdcCodeMax         = 56
	DbgAdcBitOr           = 57
	DbgAdcBitAnd          = 58
	DbgAdcBitToggle       = 59
	DbgAdcOtrCt           = 60
	DbgSampleCaptureBase  = 64
	DbgSampleCaptureCount = 128
	DbgRawCaptureBase     = 192
	DbgRawCaptureCount    = 64
)

// RegisterReader abstracts AXI register access.
// MemReader implements this via /dev/mem mmap.
// MockReader implements this for testing.
type RegisterReader interface {
	Read32(offset uint32) uint32
	Write32(offset uint32, value uint32)
	Close() error
}

// ReadDbg reads a debug counter by writing the index and reading the data register.
func ReadDbg(r RegisterReader, index uint32) uint32 {
	r.Write32(RegDbgIndex, index)
	return r.Read32(RegDbgData)
}

// ConfigWriter provides read-modify-write access to the CONFIG register
// for runtime-adjustable detector thresholds.
type ConfigWriter struct {
	r RegisterReader
}

func NewConfigWriter(r RegisterReader) *ConfigWriter {
	return &ConfigWriter{r: r}
}

func (c *ConfigWriter) SetQuietScoreShift(val uint32) error {
	if val > 7 {
		return fmt.Errorf("quiet_score_shift %d out of range (0-7)", val)
	}
	cur := c.r.Read32(RegConfig)
	cur = (cur &^ ConfigQuietScoreShiftMask) | (val & ConfigQuietScoreShiftMask)
	c.r.Write32(RegConfig, cur)
	return nil
}

func (c *ConfigWriter) SetSnrRatioShift(val uint32) error {
	if val > 7 {
		return fmt.Errorf("snr_ratio_shift %d out of range (0-7)", val)
	}
	cur := c.r.Read32(RegConfig)
	cur = (cur &^ ConfigSnrRatioShiftMask) | ((val << 3) & ConfigSnrRatioShiftMask)
	c.r.Write32(RegConfig, cur)
	return nil
}

func (c *ConfigWriter) SetHoldoff(val uint32) error {
	if val > 1023 {
		return fmt.Errorf("holdoff %d out of range (0-1023)", val)
	}
	cur := c.r.Read32(RegConfig)
	cur = (cur &^ ConfigHoldoffMask) | ((val << ConfigHoldoffShift) & ConfigHoldoffMask)
	c.r.Write32(RegConfig, cur)
	return nil
}

func (c *ConfigWriter) SetMessageDelay(val uint32) error {
	if val > 255 {
		return fmt.Errorf("message_delay %d out of range (0-255)", val)
	}
	cur := c.r.Read32(RegConfig)
	cur = (cur &^ ConfigMessageDelayMask) | ((val << ConfigMessageDelayShift) & ConfigMessageDelayMask)
	c.r.Write32(RegConfig, cur)
	return nil
}

func (c *ConfigWriter) SetOutputTap(val uint32) error {
	if val > 255 {
		return fmt.Errorf("output_tap %d out of range (0-255)", val)
	}
	cur := c.r.Read32(RegConfig)
	cur = (cur &^ ConfigOutputTapMask) | ((val << ConfigOutputTapShift) & ConfigOutputTapMask)
	c.r.Write32(RegConfig, cur)
	return nil
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

// DF returns the downlink format (0-31) from the first byte.
func (m Message) DF() uint8 {
	return m.Bytes[0] >> 3
}

// ICAO returns the 24-bit ICAO address from the AA field (bytes 1-3).
// Only meaningful for DF11/17/18 where the address is in the message body.
// For address/parity frames (DF0/4/5/16/20/21), use crc.Checksum instead.
func (m Message) ICAO() uint32 {
	return uint32(m.Bytes[1])<<16 | uint32(m.Bytes[2])<<8 | uint32(m.Bytes[3])
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
// directly, so MSG_DATA_0[7:0] contains the FIRST transmitted byte and
// MSG_DATA_3[15:0] contains the final tail bytes. We extract all 14 bytes
// from the registers in msg_bits order, then reverse to get standard
// Mode-S order.
func (r RawMessage) Decode() Message {
	var m Message
	m.TOA = uint64(r.ToaHi)<<32 | uint64(r.ToaLo)
	m.RPL = r.RPL & 0x00FFFFFF

	// Extract 14 bytes from msg_bits in MSB-first order.
	// word3 only contributes 2 bytes (bits [111:96], lower 16 bits of the word).
	// word2..word0 each contribute 4 bytes.
	var fpga [14]byte
	fpga[0] = byte(r.Data[3] >> 8)   // msg_bits[111:104]
	fpga[1] = byte(r.Data[3])        // msg_bits[103:96]
	fpga[2] = byte(r.Data[2] >> 24)  // msg_bits[95:88]
	fpga[3] = byte(r.Data[2] >> 16)  // msg_bits[87:80]
	fpga[4] = byte(r.Data[2] >> 8)   // msg_bits[79:72]
	fpga[5] = byte(r.Data[2])        // msg_bits[71:64]
	fpga[6] = byte(r.Data[1] >> 24)  // msg_bits[63:56]
	fpga[7] = byte(r.Data[1] >> 16)  // msg_bits[55:48]
	fpga[8] = byte(r.Data[1] >> 8)   // msg_bits[47:40]
	fpga[9] = byte(r.Data[1])        // msg_bits[39:32]
	fpga[10] = byte(r.Data[0] >> 24) // msg_bits[31:24]
	fpga[11] = byte(r.Data[0] >> 16) // msg_bits[23:16]
	fpga[12] = byte(r.Data[0] >> 8)  // msg_bits[15:8]
	fpga[13] = byte(r.Data[0])       // msg_bits[7:0]

	// Reverse to get standard Mode-S byte order (first transmitted first).
	for i := 0; i < 7; i++ {
		m.Bytes[i], m.Bytes[13-i] = fpga[13-i], fpga[i]
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

// ReadPpsStable reads the PPS state with a count-stable retry loop.
// If a PPS edge lands between the individual register reads, the count
// will have changed and we retry. PPS fires once per second; MMIO reads
// take microseconds, so this virtually never loops more than once.
func ReadPpsStable(r RegisterReader) PpsState {
	for {
		c1 := r.Read32(RegPpsCount)
		lo := r.Read32(RegPpsCtrLo)
		hi := r.Read32(RegPpsCtrHi)
		c2 := r.Read32(RegPpsCount)
		if c1 == c2 {
			return PpsState{Count: c1, CounterLo: lo, CounterHi: hi}
		}
	}
}
