package tracker

import (
	"fmt"
	"sync"
	"time"

	"kreklow.us/go/go-adsb/adsb"

	"github.com/plane-watcher/plane-feeder/internal/crc"
	"github.com/plane-watcher/plane-feeder/internal/regs"
)

const expiryTTL = 60 * time.Second

type Aircraft struct {
	ICAO     uint32    `json:"icao"`
	Callsign string    `json:"callsign,omitempty"`
	Altitude int64     `json:"altitude,omitempty"`
	Lat      float64   `json:"lat,omitempty"`
	Lon      float64   `json:"lon,omitempty"`
	Squawk   string    `json:"squawk,omitempty"`
	Signal   uint8     `json:"signal"`
	Seen     time.Time `json:"seen"`
	Messages uint64    `json:"messages"`
}

type Tracker struct {
	mu       sync.RWMutex
	aircraft map[uint32]*Aircraft
	refLat   float64
	refLon   float64
}

func New(lat, lon float64) *Tracker {
	return &Tracker{
		aircraft: make(map[uint32]*Aircraft),
		refLat:   lat,
		refLon:   lon,
	}
}

// RefPos returns the receiver reference position.
func (t *Tracker) RefPos() (lat, lon float64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.refLat, t.refLon
}

// SetRefPos updates the receiver reference position used for local CPR decode.
func (t *Tracker) SetRefPos(lat, lon float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refLat = lat
	t.refLon = lon
}

// Run consumes messages from ch until it is closed.
func (t *Tracker) Run(ch <-chan regs.Message) {
	for msg := range ch {
		t.process(msg)
	}
}

// Snapshot returns a copy of the current aircraft map.
func (t *Tracker) Snapshot() map[uint32]Aircraft {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[uint32]Aircraft, len(t.aircraft))
	for k, v := range t.aircraft {
		out[k] = *v
	}
	return out
}

// Count returns the number of tracked aircraft.
func (t *Tracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.aircraft)
}

// Expire removes aircraft not seen within the TTL.
func (t *Tracker) Expire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for icao, ac := range t.aircraft {
		if now.Sub(ac.Seen) > expiryTTL {
			delete(t.aircraft, icao)
		}
	}
}

func (t *Tracker) process(msg regs.Message) {
	df := msg.DF()

	var icao uint32
	switch df {
	case 11, 17, 18:
		icao = msg.ICAO()
	case 0, 4, 5, 16, 20, 21:
		icao = crc.Checksum(msg.Bytes[:msg.Len], msg.Len*8)
	default:
		return
	}

	signal := regs.RplToSignalByte(msg.RPL)

	t.mu.Lock()
	ac, ok := t.aircraft[icao]
	if !ok {
		ac = &Aircraft{ICAO: icao}
		t.aircraft[icao] = ac
	}
	ac.Seen = time.Now()
	ac.Messages++
	ac.Signal = signal
	t.mu.Unlock()

	// Decode fields via go-adsb (best-effort).
	var adsbMsg adsb.Message
	if err := adsbMsg.UnmarshalBinary(msg.Bytes[:msg.Len]); err != nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if call, err := adsbMsg.Call(); err == nil {
		ac.Callsign = call
	}

	if alt, err := adsbMsg.Alt(); err == nil {
		ac.Altitude = alt
	}

	if sqk, err := adsbMsg.Sqk(); err == nil && len(sqk) == 4 {
		ac.Squawk = fmt.Sprintf("%d%d%d%d", sqk[0], sqk[1], sqk[2], sqk[3])
	}

	if cprData, err := adsbMsg.CPR(); err == nil {
		refLat, refLon := t.refLat, t.refLon
		// Skip local CPR decode when we have no valid reference position —
		// decoding against (0, 0) produces nonsense near null island.
		if refLat != 0 || refLon != 0 {
			if coord, err := cprData.DecodeLocal([]float64{refLat, refLon}); err == nil {
				ac.Lat = coord[0]
				ac.Lon = coord[1]
			}
		}
	}
}
