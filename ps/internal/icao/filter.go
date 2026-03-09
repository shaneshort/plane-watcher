package icao

import "time"

// Filter tracks recently-seen ICAO addresses from CRC-validated messages
// (DF11/17/18). Address/parity frames (DF0/4/5/16/20/21) are accepted
// only if the CRC-derived address matches a tracked entry.
//
// This mirrors the icaoFilterTest/icaoFilterAdd approach used by
// dump1090 and readsb.
type Filter struct {
	addrs map[uint32]time.Time
	ttl   time.Duration
}

// NewFilter creates a filter with the given time-to-live for entries.
// 60 seconds is the conventional value used by dump1090 and readsb.
func NewFilter(ttl time.Duration) *Filter {
	return &Filter{
		addrs: make(map[uint32]time.Time),
		ttl:   ttl,
	}
}

// Add records an ICAO address (or refreshes its timestamp).
func (f *Filter) Add(addr uint32) {
	f.addrs[addr] = time.Now()
}

// Test returns true if the address was recently seen.
func (f *Filter) Test(addr uint32) bool {
	t, ok := f.addrs[addr]
	if !ok {
		return false
	}
	if time.Since(t) > f.ttl {
		delete(f.addrs, addr)
		return false
	}
	return true
}

// Count returns the number of tracked addresses.
func (f *Filter) Count() int {
	return len(f.addrs)
}

// Expire removes entries older than the TTL.
func (f *Filter) Expire() {
	now := time.Now()
	for addr, t := range f.addrs {
		if now.Sub(t) > f.ttl {
			delete(f.addrs, addr)
		}
	}
}
