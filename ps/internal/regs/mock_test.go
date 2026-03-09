package regs

import "testing"

func TestMockReaderBasic(t *testing.T) {
	mock := NewMockReader()

	mock.Push(RawMessage{
		Data:  [4]uint32{0x01020304, 0x05060708, 0x090A0B0C, 0x0D0E0000},
		ToaLo: 100, ToaHi: 0, RPL: 5000,
	})

	status := mock.Read32(RegStatus)
	if status&StatusNotEmpty == 0 {
		t.Error("expected not_empty after push")
	}

	raw := ReadMessage(mock)
	if raw.Data[0] != 0x01020304 {
		t.Errorf("data[0]: got 0x%08X, want 0x01020304", raw.Data[0])
	}
	if raw.RPL != 5000 {
		t.Errorf("RPL: got %d, want 5000", raw.RPL)
	}

	status = mock.Read32(RegStatus)
	if status&StatusNotEmpty != 0 {
		t.Error("expected empty after pop")
	}
}
