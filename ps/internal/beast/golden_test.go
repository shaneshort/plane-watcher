package beast_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/beast"
	"github.com/plane-watcher/plane-feeder/internal/regs"
)

// Golden end-to-end fixtures: RawMessage -> Decode() -> beast.EncodeData() -> exact bytes.
// These lock down the full AXI-to-wire contract. Any change to byte order,
// timestamp scaling, signal mapping, or escaping will break them.

func TestGoldenLongDF17(t *testing.T) {
	// Known DF17 from sim: 8D75804B580FF2CF7E9BA6F701D0
	// AXI registers verified against two_sequential.dat TB output.
	raw := regs.RawMessage{
		Data:  [4]uint32{0x4B80758D, 0xCFF20F58, 0xF7A69B7E, 0x0000D001},
		ToaLo: 1181, ToaHi: 0, RPL: 753992,
	}

	msg := raw.Decode()
	frame := beast.EncodeData(beast.ModeBeast12MHz, msg, nil)

	// ts12 = floor(1181 * 12 / 100) = 141 = 0x00000000008D
	// signal = sqrt(753992 / 2621440) * 255 = 136 = 0x88
	// msg bytes: 8D 75 80 4B 58 0F F2 CF 7E 9B A6 F7 01 D0
	want := mustHex("1A 33 00 00 00 00 00 8D 88 8D 75 80 4B 58 0F F2 CF 7E 9B A6 F7 01 D0")

	if !bytes.Equal(frame, want) {
		t.Errorf("long DF17 golden frame mismatch\n got: %s\nwant: %s", fmtHex(frame), fmtHex(want))
	}
}

func TestGoldenShortDF0(t *testing.T) {
	// Known DF0 from sim: 02E1950450 65A3
	// AXI registers verified against single_short.dat TB output.
	raw := regs.RawMessage{
		Data:  [4]uint32{0x0495E102, 0x00A36550, 0x00000000, 0x00000000},
		ToaLo: 2000, ToaHi: 0, RPL: 0x100000,
	}

	msg := raw.Decode()
	frame := beast.EncodeData(beast.ModeBeast12MHz, msg, nil)

	// ts12 = floor(2000 * 12 / 100) = 240 = 0x0000000000F0
	// signal = sqrt(0x100000 / 2621440) * 255 = 161 = 0xA1
	// msg bytes (7): 02 E1 95 04 50 65 A3
	want := mustHex("1A 32 00 00 00 00 00 F0 A1 02 E1 95 04 50 65 A3")

	if !bytes.Equal(frame, want) {
		t.Errorf("short DF0 golden frame mismatch\n got: %s\nwant: %s", fmtHex(frame), fmtHex(want))
	}
}

func TestGoldenEscaping(t *testing.T) {
	// Synthetic message where first byte is 0x1A (DF3, short).
	// Verifies that 0x1A in the message payload is doubled on the wire.
	raw := regs.RawMessage{
		Data:  [4]uint32{0x0000001A, 0x00000000, 0x00000000, 0x00000000},
		ToaLo: 1000, ToaHi: 0, RPL: 0x200000,
	}

	msg := raw.Decode()
	frame := beast.EncodeData(beast.ModeBeast12MHz, msg, nil)

	// ts12 = floor(1000 * 12 / 100) = 120 = 0x000000000078
	// signal = sqrt(0x200000 / 2621440) * 255 = 228 = 0xE4
	// msg bytes (7): 1A 00 00 00 00 00 00
	// The 0x1A at msg[0] must be escaped -> 1A 1A on the wire.
	want := mustHex("1A 32 00 00 00 00 00 78 E4 1A 1A 00 00 00 00 00 00")

	if !bytes.Equal(frame, want) {
		t.Errorf("escaping golden frame mismatch\n got: %s\nwant: %s", fmtHex(frame), fmtHex(want))
	}
}

func mustHex(s string) []byte {
	compact := ""
	for _, c := range s {
		if c != ' ' {
			compact += string(c)
		}
	}
	b, err := hex.DecodeString(compact)
	if err != nil {
		panic(fmt.Sprintf("mustHex(%q): %v", s, err))
	}
	return b
}

func fmtHex(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " "
		}
		s += p
	}
	return s
}
