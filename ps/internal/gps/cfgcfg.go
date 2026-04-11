package gps

import (
	"context"
	"encoding/binary"
	"fmt"
)

// CFG-CFG (0x06 0x09) helpers. See u-blox 8/M8 Receiver Description
// (UBX-13003221) §32.10.3. Payload is 12 bytes, or 13 with the optional
// deviceMask trailing byte.
//
// Layout:
//
//	 0 X4 clearMask    sub-sections to reset to default
//	 4 X4 saveMask     sub-sections to save to non-volatile memory
//	 8 X4 loadMask     sub-sections to load from non-volatile memory
//	12 X1 deviceMask   destination memory devices (BBR, Flash, EEPROM, ...)
//
// Masks bits (u-blox UBX-13003221 §32.10.3):
//
//	bit 0  ioPort
//	bit 1  msgConf
//	bit 2  infMsg
//	bit 3  navConf
//	bit 4  rxmConf
//	bit 8  senConf
//	bit 9  rinvConf
//	bit 10 antConf
//	bit 11 logConf
//	bit 12 ftsConf
//
// deviceMask bits:
//
//	bit 0  devBBR       (battery-backed RAM)
//	bit 1  devFlash
//	bit 2  devEEPROM
//	bit 4  devSpiFlash

const (
	// cfgCfgSaveAllSections covers every documented saveMask bit
	// (0-4 and 8-12). Safe to send blanket: bits the device doesn't
	// understand are ignored.
	cfgCfgSaveAllSections uint32 = 0x00001F1F

	// cfgCfgDeviceBBRFlash saves to both battery-backed RAM and internal
	// flash. BBR persists across warm resets, flash persists across
	// power cycles. The LEA-M8T has internal flash per its MON-VER
	// extensions (FIS=0xEF4015), so this is the right combination for
	// persistence.
	cfgCfgDeviceBBRFlash uint8 = 0x03
)

// BuildCfgCfgSaveAll returns a 13-byte CFG-CFG payload that saves every
// configuration section to BBR + Flash. clearMask and loadMask are zero
// (we don't clear or load, only save).
func BuildCfgCfgSaveAll() []byte {
	pl := make([]byte, 13)
	// clearMask[0:4] = 0
	binary.LittleEndian.PutUint32(pl[4:8], cfgCfgSaveAllSections) // saveMask
	// loadMask[8:12] = 0
	pl[12] = cfgCfgDeviceBBRFlash
	return pl
}

// SaveConfig sends a CFG-CFG command that saves all config sections to
// BBR+Flash and waits for the ACK.
//
// NOTE: CFG-CFG is write-only — there's no queryable "was this saved"
// state to poll, so the verification stops at the ACK. Drain-before-send
// still protects against pre-buffered stale ACKs from earlier writes.
// If the ACK is falsely satisfied by a stale frame (post-send race), the
// worst case is we THINK we saved when we didn't; a reboot will reveal
// that immediately.
func SaveConfig(ctx context.Context, dc *DeviceClient) error {
	frame := Frame{
		Class:   ClassCFG,
		ID:      IDCfgCFG,
		Payload: BuildCfgCfgSaveAll(),
	}
	if err := dc.SendAndAwaitACK(ctx, frame); err != nil {
		return fmt.Errorf("CFG-CFG save: %w", err)
	}
	return nil
}
