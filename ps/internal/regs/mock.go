package regs

import "sync"

// MockReader implements RegisterReader with an in-memory FIFO
// for development and testing without hardware.
type MockReader struct {
	mu      sync.Mutex
	queue   []RawMessage
	current RawMessage
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

func (m *MockReader) Write32(offset uint32, value uint32) {}

func (m *MockReader) Close() error { return nil }
