//go:build linux

package regs

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
)

// MemReader accesses FPGA registers via /dev/mem mmap.
// Only works on Linux (Zynq PS).
type MemReader struct {
	file    *os.File
	mapping []byte // original page-aligned mmap (for Munmap)
	regs    []byte // offset into mapping at the AXI base address
}

// NewMemReader opens /dev/mem and mmaps the AXI register region.
func NewMemReader(baseAddr uint64) (*MemReader, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/mem: %w (try running as root)", err)
	}

	pageSize := uint64(syscall.Getpagesize())
	pageBase := baseAddr & ^(pageSize - 1)
	offset := baseAddr - pageBase
	mapSize := offset + 256

	mapping, err := syscall.Mmap(int(f.Fd()), int64(pageBase),
		int(mapSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap 0x%08X: %w", baseAddr, err)
	}

	return &MemReader{
		file:    f,
		mapping: mapping,
		regs:    mapping[offset:],
	}, nil
}

func (m *MemReader) Read32(offset uint32) uint32 {
	return binary.LittleEndian.Uint32(m.regs[offset : offset+4])
}

func (m *MemReader) Write32(offset uint32, value uint32) {
	binary.LittleEndian.PutUint32(m.regs[offset:offset+4], value)
}

func (m *MemReader) Close() error {
	if m.mapping != nil {
		syscall.Munmap(m.mapping) // must pass the original page-aligned slice
	}
	if m.file != nil {
		return m.file.Close()
	}
	return nil
}
