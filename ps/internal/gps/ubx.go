// Package gps provides a gpsd client and UBX (u-blox binary protocol) framing
// helpers for interacting with u-blox GNSS receivers through a running gpsd
// instance. See docs/plans/2026-04-10-ubx-gpsd-tool-design.md for the full
// design.
package gps

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// UBX sync bytes that prefix every frame on the wire.
const (
	SyncByte1 = 0xB5
	SyncByte2 = 0x62
)

// UBX message classes used by this package. Values come from the u-blox
// interface description (M8 and F9 generations).
const (
	ClassNAV = 0x01
	ClassACK = 0x05
	ClassCFG = 0x06
	ClassMON = 0x0A
	ClassTIM = 0x0D
)

// Selected UBX message IDs (paired with their class).
const (
	IDAckACK    = 0x01 // ACK-ACK
	IDAckNAK    = 0x00 // ACK-NAK
	IDCfgCFG    = 0x09 // CFG-CFG  (clear/save/load config)
	IDCfgNAV5   = 0x24 // CFG-NAV5 (navigation engine settings)
	IDCfgTP5    = 0x31 // CFG-TP5  (time pulse settings)
	IDCfgTMODE2 = 0x3D // CFG-TMODE2 (M8 generation)
	IDCfgTMODE3 = 0x71 // CFG-TMODE3 (F9 generation)
	IDMonHW     = 0x09 // MON-HW (hardware status, antenna)
	IDMonVER    = 0x04 // MON-VER
	IDNavClock  = 0x22 // NAV-CLOCK (clock solution: bias/drift/tAcc/fAcc)
	IDNavDOP    = 0x04 // NAV-DOP (dilution of precision)
	IDNavSVIN   = 0x3B // NAV-SVIN (F9)
	IDTimSVIN   = 0x04 // TIM-SVIN (M8)
)

// Frame is a decoded UBX message. It carries the class/ID and the raw payload
// bytes — payload interpretation lives in the caller (or in helper functions
// in this package for the message types we care about).
type Frame struct {
	Class   byte
	ID      byte
	Payload []byte
}

// Encode returns the full on-the-wire bytes for the frame: sync bytes,
// class, ID, little-endian length, payload, then the Fletcher-8 checksum.
func (f Frame) Encode() []byte {
	if len(f.Payload) > 0xFFFF {
		// Payload longer than the wire format can express. Callers
		// constructing frames inside this package never trip this; if a
		// future caller does, return a clearly-invalid frame so a test
		// will catch it rather than silently truncating.
		return nil
	}
	out := make([]byte, 0, 8+len(f.Payload))
	out = append(out, SyncByte1, SyncByte2, f.Class, f.ID)
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(f.Payload)))
	out = append(out, lenBuf[:]...)
	out = append(out, f.Payload...)
	ckA, ckB := ChecksumFletcher8(out[2:]) // class .. payload[end]
	out = append(out, ckA, ckB)
	return out
}

// Decode parses a single UBX frame from the start of b. It returns the parsed
// frame and the number of bytes consumed. The remainder of b (if any) is left
// for the caller to deal with — useful when a single buffer holds multiple
// concatenated frames.
//
// Decode validates the sync bytes, the declared length, and the Fletcher-8
// checksum. A truncated, sync-mismatched, or checksum-mismatched buffer
// returns a non-nil error and a zero Frame.
func Decode(b []byte) (Frame, int, error) {
	if len(b) < 8 {
		return Frame{}, 0, fmt.Errorf("ubx: frame too short: %d bytes", len(b))
	}
	if b[0] != SyncByte1 || b[1] != SyncByte2 {
		return Frame{}, 0, fmt.Errorf("ubx: bad sync bytes %02x %02x", b[0], b[1])
	}
	class := b[2]
	id := b[3]
	payloadLen := int(binary.LittleEndian.Uint16(b[4:6]))
	frameLen := 8 + payloadLen
	if len(b) < frameLen {
		return Frame{}, 0, fmt.Errorf("ubx: frame truncated: have %d, need %d", len(b), frameLen)
	}
	payload := b[6 : 6+payloadLen]
	ckA, ckB := ChecksumFletcher8(b[2 : 6+payloadLen])
	if b[6+payloadLen] != ckA || b[6+payloadLen+1] != ckB {
		return Frame{}, 0, fmt.Errorf("ubx: checksum mismatch: got %02x %02x want %02x %02x",
			b[6+payloadLen], b[6+payloadLen+1], ckA, ckB)
	}
	// Copy the payload so the returned Frame is independent of the input
	// buffer (callers may reuse b for the next read).
	pl := make([]byte, payloadLen)
	copy(pl, payload)
	return Frame{Class: class, ID: id, Payload: pl}, frameLen, nil
}

// ChecksumFletcher8 computes the 8-bit Fletcher checksum used by UBX.
// The input is everything from the class byte through the end of the payload
// (sync bytes excluded). See u-blox interface description, "UBX Checksum".
func ChecksumFletcher8(b []byte) (ckA, ckB byte) {
	for _, c := range b {
		ckA += c
		ckB += ckA
	}
	return ckA, ckB
}

// IsAck reports whether f is ACK-ACK or ACK-NAK and (when true) returns the
// class/ID of the message it acknowledges. The ack payload is two bytes:
// the acknowledged class and ID.
func (f Frame) IsAck() (ok bool, ackClass, ackID byte, isNak bool) {
	if f.Class != ClassACK || len(f.Payload) != 2 {
		return false, 0, 0, false
	}
	return true, f.Payload[0], f.Payload[1], f.ID == IDAckNAK
}

// PollFrame returns a poll request frame for the given class/ID with an empty
// payload — the standard way to ask a u-blox receiver to emit the current
// state of a message.
func PollFrame(class, id byte) Frame {
	return Frame{Class: class, ID: id, Payload: nil}
}

// ErrShortFrame is returned by helpers that need a payload longer than what
// the receiver supplied. Wrap-friendly so callers can errors.Is.
var ErrShortFrame = errors.New("ubx: payload shorter than expected")
