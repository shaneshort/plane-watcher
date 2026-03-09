//go:build !linux

package regs

import "fmt"

// NewMemReader is unavailable on non-Linux platforms.
func NewMemReader(baseAddr uint64) (*MemReader, error) {
	return nil, fmt.Errorf("/dev/mem not available on this platform")
}

// MemReader stub for non-Linux builds.
type MemReader struct{}

func (m *MemReader) Read32(offset uint32) uint32         { return 0 }
func (m *MemReader) Write32(offset uint32, value uint32) {}
func (m *MemReader) Close() error                        { return nil }
