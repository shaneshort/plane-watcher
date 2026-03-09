package crc

// Mode-S CRC-24 generator polynomial (ITU Annex 10, Vol IV).
const generatorPoly = 0xFFF409

var table [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		c := uint32(i) << 16
		for j := 0; j < 8; j++ {
			if c&0x800000 != 0 {
				c = (c << 1) ^ generatorPoly
			} else {
				c = c << 1
			}
		}
		table[i] = c & 0x00FFFFFF
	}
}

// Checksum computes the Mode-S CRC-24 syndrome over a message.
//
// For DF17/18 (zero-remainder CRC), the result is 0 for valid messages.
// For DF11, the result's lower 7 bits are the interrogator ID (IID);
// upper 17 bits should be 0 for valid messages.
// For address/parity frames (DF0/4/5/16/20/21), the result is the
// sender's 24-bit ICAO address.
//
// bits must be 56 (short, 7 bytes) or 112 (long, 14 bytes).
// This is a direct port of modesChecksum() from dump1090/readsb.
func Checksum(msg []byte, bits int) uint32 {
	n := bits / 8
	var rem uint32
	for i := 0; i < n-3; i++ {
		rem = (rem << 8) ^ table[msg[i]^byte((rem&0xFF0000)>>16)]
		rem &= 0xFFFFFF
	}
	rem ^= uint32(msg[n-3])<<16 | uint32(msg[n-2])<<8 | uint32(msg[n-1])
	return rem
}
