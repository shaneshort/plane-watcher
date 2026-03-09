package crc

import (
	"encoding/hex"
	"testing"
)

func TestChecksum_DF17_Valid(t *testing.T) {
	// Known valid DF17 message from FPGA test vectors:
	// 8D75804B580FF2CF7E9BA6 with CRC F701D0
	raw, _ := hex.DecodeString("8D75804B580FF2CF7E9BA6F701D0")
	got := Checksum(raw, 112)
	if got != 0 {
		t.Errorf("Checksum = 0x%06X, want 0x000000 (valid DF17)", got)
	}
}

func TestChecksum_DF17_ExtractCRC(t *testing.T) {
	// Same message with parity bytes zeroed — CRC should equal the original parity.
	raw, _ := hex.DecodeString("8D75804B580FF2CF7E9BA6000000")
	got := Checksum(raw, 112)
	if got != 0xF701D0 {
		t.Errorf("Checksum = 0x%06X, want 0xF701D0", got)
	}
}

func TestChecksum_Short_RoundTrip(t *testing.T) {
	// Build a valid 56-bit message by setting parity to computed CRC.
	raw := []byte{0x28, 0x00, 0x1D, 0x88, 0x00, 0x00, 0x00} // DF5 (0x28 >> 3 = 5)
	c := Checksum(raw, 56)
	raw[4] = byte(c >> 16)
	raw[5] = byte(c >> 8)
	raw[6] = byte(c)
	got := Checksum(raw, 56)
	if got != 0 {
		t.Errorf("Checksum = 0x%06X, want 0x000000 after setting parity", got)
	}
}

func TestChecksum_AddressParity(t *testing.T) {
	// For address/parity frames, CRC syndrome = ICAO address.
	// Build a DF4 message (0x20 >> 3 = 4) with parity = CRC XOR ICAO.
	icaoAddr := uint32(0x75804B)
	raw := []byte{0x20, 0x00, 0x1D, 0x88, 0x00, 0x00, 0x00}
	// Compute CRC with zero parity
	c := Checksum(raw, 56)
	// Set parity = CRC XOR ICAO so that Checksum returns the ICAO
	ap := c ^ icaoAddr
	raw[4] = byte(ap >> 16)
	raw[5] = byte(ap >> 8)
	raw[6] = byte(ap)
	got := Checksum(raw, 56)
	if got != icaoAddr {
		t.Errorf("Checksum = 0x%06X, want 0x%06X (ICAO address)", got, icaoAddr)
	}
}
