package gps

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestChecksumFletcher8_KnownVectors checks the checksum routine against
// hand-verified vectors from u-blox documentation. Both vectors are MON-VER
// polls (class 0x0A, id 0x04, no payload) — the canonical "smallest UBX
// frame" used as a sanity check throughout u-blox app notes.
func TestChecksumFletcher8_KnownVectors(t *testing.T) {
	tests := []struct {
		name string
		// input is everything from class byte through end of payload
		input  []byte
		wantA  byte
		wantB  byte
	}{
		{
			name:  "MON-VER poll (no payload)",
			input: []byte{0x0A, 0x04, 0x00, 0x00},
			wantA: 0x0E,
			wantB: 0x34,
		},
		{
			name:  "ACK-ACK for CFG-TMODE3",
			input: []byte{0x05, 0x01, 0x02, 0x00, 0x06, 0x71},
			wantA: 0x7F,
			wantB: 0xA8,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotA, gotB := ChecksumFletcher8(tt.input)
			if gotA != tt.wantA || gotB != tt.wantB {
				t.Errorf("ChecksumFletcher8(%x) = %02x %02x, want %02x %02x",
					tt.input, gotA, gotB, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestEncode_MonVerPoll(t *testing.T) {
	f := PollFrame(ClassMON, IDMonVER)
	got := f.Encode()
	want, _ := hex.DecodeString("b5620a0400000e34")
	if !bytes.Equal(got, want) {
		t.Errorf("MON-VER poll encode = %x, want %x", got, want)
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	tests := []Frame{
		{Class: ClassMON, ID: IDMonVER, Payload: nil},
		{Class: ClassCFG, ID: IDCfgTMODE3, Payload: bytes.Repeat([]byte{0xAA}, 40)},
		{Class: ClassNAV, ID: IDNavSVIN, Payload: []byte{1, 2, 3, 4, 5}},
		{Class: ClassACK, ID: IDAckACK, Payload: []byte{0x06, 0x71}},
	}
	for _, f := range tests {
		t.Run(hex.EncodeToString([]byte{f.Class, f.ID}), func(t *testing.T) {
			wire := f.Encode()
			got, n, err := Decode(wire)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != len(wire) {
				t.Errorf("consumed %d bytes, expected %d", n, len(wire))
			}
			if got.Class != f.Class || got.ID != f.ID {
				t.Errorf("class/id mismatch: got %02x/%02x, want %02x/%02x",
					got.Class, got.ID, f.Class, f.ID)
			}
			if !bytes.Equal(got.Payload, f.Payload) {
				t.Errorf("payload mismatch: got %x, want %x", got.Payload, f.Payload)
			}
		})
	}
}

func TestDecode_RejectsBadInput(t *testing.T) {
	good := PollFrame(ClassMON, IDMonVER).Encode()

	tests := []struct {
		name string
		in   []byte
	}{
		{"too short", []byte{0xB5, 0x62}},
		{"bad sync 1", append([]byte{0x00, 0x62}, good[2:]...)},
		{"bad sync 2", append([]byte{0xB5, 0x00}, good[2:]...)},
		{"truncated payload", []byte{0xB5, 0x62, 0x06, 0x71, 0x28, 0x00, 0x01}},
		{"bad checksum", func() []byte {
			b := append([]byte(nil), good...)
			b[len(b)-1] ^= 0xFF
			return b
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Decode(tt.in)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestFrame_IsAck(t *testing.T) {
	ackACK := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{0x06, 0x71}}
	ok, c, id, nak := ackACK.IsAck()
	if !ok || c != 0x06 || id != 0x71 || nak {
		t.Errorf("ACK-ACK detection wrong: ok=%v c=%02x id=%02x nak=%v", ok, c, id, nak)
	}

	ackNAK := Frame{Class: ClassACK, ID: IDAckNAK, Payload: []byte{0x06, 0x71}}
	ok, c, id, nak = ackNAK.IsAck()
	if !ok || c != 0x06 || id != 0x71 || !nak {
		t.Errorf("ACK-NAK detection wrong: ok=%v c=%02x id=%02x nak=%v", ok, c, id, nak)
	}

	nonAck := Frame{Class: ClassNAV, ID: IDNavSVIN, Payload: []byte{0x00, 0x00}}
	if ok, _, _, _ := nonAck.IsAck(); ok {
		t.Errorf("non-ack frame reported as ack")
	}

	wrongLen := Frame{Class: ClassACK, ID: IDAckACK, Payload: []byte{0x06}}
	if ok, _, _, _ := wrongLen.IsAck(); ok {
		t.Errorf("malformed ack (1-byte payload) reported as ack")
	}
}
