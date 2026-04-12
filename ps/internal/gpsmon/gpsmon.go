// Package gpsmon is the long-lived GNSS/PPS monitor used by plane-feeder
// to populate the GPS status dashboard. It owns exactly one
// gps.Client/DeviceClient for the lifetime of the process and publishes
// periodic Snapshots atomically so HTTP handlers can read the latest
// state without blocking.
//
// Ownership boundary: nothing else in plane-feeder should hold a
// gps.Client pointing at the same device. Action handlers that write to
// the receiver (trigger survey-in, save config, etc.) must open their
// own short-lived gps.Client instead — see the ubx CLI for the pattern.
// gps.DeviceClient is explicitly single-consumer, and routing a second
// caller onto the same handle will race the collector's waiter.
package gpsmon

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/gps"
)

// Options configures a Collector.
type Options struct {
	// GpsdAddr is the TCP address of the gpsd daemon, e.g. "localhost:2947".
	GpsdAddr string
	// DevicePath is the serial device path gpsd reports for the
	// receiver (e.g. "/dev/ttyPS1"). If empty, Run auto-picks the
	// first device reported by gpsd.
	DevicePath string
	// PollInterval is how often the Collector polls the dynamic UBX
	// status messages (MON-HW, NAV-CLOCK, NAV-DOP, TIM-SVIN). TPV and
	// SKY update themselves via the atomic cache on DeviceClient,
	// independent of this interval.
	PollInterval time.Duration
	// Logger receives transport and poll error messages. Required.
	Logger *log.Logger
	// HistoryRetention controls how far back the time-series history
	// ring buffer retains samples. Zero defaults to one hour.
	HistoryRetention time.Duration
	// PPMFn returns the current PPS-disciplined oscillator offset in
	// parts-per-million, sampled into the history ring buffer once per
	// cycle. Wire this to pps.Watcher.Stats().OscillatorPPM from
	// plane-feeder. If nil, the history's PPM column stays zero.
	PPMFn func() float64
}

// Collector is the runtime owner of the one gps.Client used for GNSS
// monitoring inside plane-feeder.
type Collector struct {
	opts     Options
	snapshot atomic.Pointer[Snapshot]
	history  *History
}

// New creates a Collector with sensible defaults for missing Options
// fields. Call Run on the returned Collector to start the monitor loop.
func New(opts Options) *Collector {
	if opts.GpsdAddr == "" {
		opts.GpsdAddr = "localhost:2947"
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	c := &Collector{
		opts:    opts,
		history: NewHistory(opts.HistoryRetention),
	}
	// Publish an initial empty snapshot so Snapshot() always returns
	// non-nil; the first Run tick will replace it with real data.
	c.snapshot.Store(&Snapshot{})
	return c
}

// Snapshot returns the most recent Snapshot. Always non-nil. Safe to
// call from any goroutine.
func (c *Collector) Snapshot() *Snapshot {
	return c.snapshot.Load()
}

// History returns a copy of the current time-series samples. Safe to
// call from any goroutine; the underlying storage is never returned to
// the caller.
func (c *Collector) History() []Sample {
	return c.history.Snapshot()
}

// Run is the collector's main loop. It blocks until ctx is cancelled,
// so callers typically spawn it on its own goroutine. If the gpsd
// connection fails or drops, Run logs the error, publishes a snapshot
// with TransportErr populated, and retries on the next PollInterval
// tick.
func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.opts.PollInterval)
	defer ticker.Stop()

	// First cycle runs immediately so the dashboard doesn't show a
	// blank page for PollInterval seconds at startup.
	c.cycle(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.cycle(ctx)
		}
	}
}

// cycle is one pass of the collector: open (or verify) the gps.Client,
// poll the dynamic UBX messages, read the atomic TPV/SKY cache, and
// publish a new snapshot.
//
// A fresh gps.Client is opened every cycle. This is intentional: the
// collector runs on a 5-second cadence (default), each open is <100 ms,
// and the gps.Client has no reconnection state machine. Reopening per
// cycle keeps the failure semantics simple and makes transient gpsd
// restarts recover automatically on the next tick. If cycle cost ever
// becomes a problem we can cache the Client across cycles.
func (c *Collector) cycle(ctx context.Context) {
	snap := &Snapshot{
		ReceiverAt: time.Now(),
		PeriodicAt: time.Now(),
	}

	// Use a short dial deadline so a dead gpsd doesn't wedge the
	// collector for the full poll interval.
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	client, err := gps.Dial(dialCtx, c.opts.GpsdAddr)
	cancel()
	if err != nil {
		snap.TransportErr = fmt.Sprintf("dial gpsd: %v", err)
		c.snapshot.Store(snap)
		c.opts.Logger.Printf("[gpsmon] %s", snap.TransportErr)
		return
	}
	defer client.Close()

	// Resolve the target device path.
	path := c.opts.DevicePath
	if path == "" {
		devs := client.Devices()
		if len(devs) == 0 {
			snap.TransportErr = "gpsd reports no devices"
			c.snapshot.Store(snap)
			c.opts.Logger.Printf("[gpsmon] %s", snap.TransportErr)
			return
		}
		path = devs[0].Path
	}
	dc := client.Subscribe(path)

	// Step 1: MON-VER + generation detection.
	detectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	gen, mv, err := gps.DetectGeneration(detectCtx, dc)
	cancel()
	snap.Receiver.Generation = gen.String()
	if err != nil {
		snap.ReceiverErr = fmt.Sprintf("MON-VER: %v", err)
		c.opts.Logger.Printf("[gpsmon] %s", snap.ReceiverErr)
	} else {
		snap.Receiver.SwVersion = mv.SwVersion
		snap.Receiver.HwVersion = mv.HwVersion
		snap.Receiver.Extensions = append([]string(nil), mv.Extensions...)
	}

	// Everything that follows depends on a known generation, but we
	// keep going on errors and stash them per field. A partial snapshot
	// is still useful.

	// Step 2: CFG-TMODE, CFG-NAV5, CFG-TP5 for the Receiver card. The
	// TMODE payload is stashed in a local so step 3 (after polling
	// SVIN) can extract fixed-mode ECEF coordinates from it — TIM-SVIN
	// returns zeros in fixed mode, so we fall back to the TMODE
	// payload's own ecefX/Y/Z fields for the dashboard display.
	var tmodeRaw []byte
	if gen != gps.GenUnknown {
		tmodeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if tmode, err := gps.PollTMODE(tmodeCtx, dc, gen); err == nil {
			snap.Receiver.TMODEMode = gps.TMODEMode(gen, tmode.Raw)
			tmodeRaw = tmode.Raw
		} else {
			snap.appendReceiverErr(fmt.Sprintf("CFG-TMODE: %v", err))
		}
		cancel()

		nav5Ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if raw, err := gps.PollNAV5(nav5Ctx, dc); err == nil {
			if parsed, err := gps.ParseNAV5(raw); err == nil {
				snap.Receiver.NAV5 = parsed
			}
		} else {
			snap.appendReceiverErr(fmt.Sprintf("CFG-NAV5: %v", err))
		}
		cancel()

		tp5Ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if raw, err := gps.PollTP5(tp5Ctx, dc); err == nil {
			if parsed, err := gps.ParseTP5(raw); err == nil {
				snap.Receiver.TP5 = parsed
			}
		} else {
			snap.appendReceiverErr(fmt.Sprintf("CFG-TP5: %v", err))
		}
		cancel()
	}

	// Step 3: periodic dynamic polls — MON-HW, NAV-CLOCK, NAV-DOP,
	// TIM-SVIN/NAV-SVIN. Each failure is captured per-field.
	hwCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if hw, err := gps.PollMonHW(hwCtx, dc); err == nil {
		snap.HW = hw
	} else {
		snap.appendPeriodicErr(fmt.Sprintf("MON-HW: %v", err))
	}
	cancel()

	clockCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if clock, err := gps.PollNavClock(clockCtx, dc); err == nil {
		snap.Clock = clock
	} else {
		snap.appendPeriodicErr(fmt.Sprintf("NAV-CLOCK: %v", err))
	}
	cancel()

	dopCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if dop, err := gps.PollNavDOP(dopCtx, dc); err == nil {
		snap.DOP = dop
	} else {
		snap.appendPeriodicErr(fmt.Sprintf("NAV-DOP: %v", err))
	}
	cancel()

	if gen != gps.GenUnknown {
		svinPoll, err := gps.SVINPollFrame(gen)
		if err == nil {
			svinCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			resp, err := dc.SendAndAwaitResponse(svinCtx, svinPoll, svinPoll.Class, svinPoll.ID)
			cancel()
			if err == nil {
				if st, err := gps.ParseSVIN(gen, resp.Payload); err == nil {
					snap.Survey = st
				} else {
					snap.appendPeriodicErr(fmt.Sprintf("SVIN parse: %v", err))
				}
			} else {
				snap.appendPeriodicErr(fmt.Sprintf("SVIN poll: %v", err))
			}
		}
		// Overlay fixed-mode coordinates on the Survey block when the
		// receiver is locked. Done AFTER the SVIN parse so we overwrite
		// the zero payload TIM-SVIN returns in fixed mode with the
		// real coordinates taken from the CFG-TMODE payload itself.
		if snap.Receiver.TMODEMode == 2 && tmodeRaw != nil {
			if x, y, z, _, _, _, accMM, ok := gps.ExtractFixedModeECEF(gen, tmodeRaw); ok {
				snap.Survey.MeanXCm = x
				snap.Survey.MeanYCm = y
				snap.Survey.MeanZCm = z
				snap.Survey.MeanAccMeters = float64(accMM) / 1000.0
				snap.Survey.Valid = true
				snap.Survey.Active = false
			}
		}
	}

	// Step 4: read the atomic TPV/SKY cache from the DeviceClient. The
	// reader goroutine has been populating these since Dial returned;
	// by this point the first ~1 second of gpsd traffic has landed.
	if tpv := dc.LatestTPV(); tpv != nil {
		snap.Fix = *tpv
		snap.FixAt = time.Now()
	}
	if sky := dc.LatestSKY(); sky != nil {
		snap.Sats = sky.Satellites
		snap.SkyDOP = *sky
		snap.SatsAt = time.Now()
	}

	c.snapshot.Store(snap)

	// Append one time-series sample per cycle. PPM comes from the
	// optional callback (plane-feeder wires it to pps.Watcher); used
	// sats come from the SKY cache we just read. Zero values are
	// recorded on missing data so the chart time axis stays dense.
	sample := Sample{TsMS: time.Now().UnixMilli()}
	if c.opts.PPMFn != nil {
		sample.OscillatorPPM = c.opts.PPMFn()
	}
	if sky := dc.LatestSKY(); sky != nil {
		sample.UsedSats = sky.USat
	}
	c.history.Append(sample)
}
