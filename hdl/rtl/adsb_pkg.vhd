-- =============================================================================
-- adsb_pkg.vhd — Central Package for ADS-B Decoder
-- =============================================================================
--
-- This package defines all shared constants, types, and parameters for the
-- FPGA-based ADS-B / Mode-S receiver pipeline. By centralising these here,
-- the entire decode pipeline automatically adapts when the sample rate,
-- decoder count, or ADC resolution changes.
--
-- The receiver decodes 1090 MHz Mode-S transponder signals, which use
-- Pulse Position Modulation (PPM) at a 1 MHz chip rate with 2 chips per
-- data bit. ADS-B (Automatic Dependent Surveillance - Broadcast) is a
-- specific application of Mode-S using Downlink Format 17 (DF17) with
-- 112-bit extended messages containing aircraft position and identity.
--
-- This design is adapted from the bladeRF-adsb project (Nuand) with
-- additions for hardware timestamping to support MLAT (Multilateration).
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

package adsb_pkg is

    -- =========================================================================
    -- Sample Rate and Timing
    -- =========================================================================
    -- The sample clock is derived from a GPSDO 10 MHz reference via PLL.
    -- 16 MHz gives exactly 8 samples per PPM chip (1 MHz chip rate), which
    -- matches the bladeRF-adsb design and provides good timing resolution.
    -- The AD9363 SDR frontend supports this rate natively.
    -- =========================================================================

    constant SAMPLE_RATE_HZ : positive := 16_000_000;

    -- Samples Per PPM chip. Mode-S uses 1 MHz PPM, so at 16 MSPS we get
    -- 16/1 = 16 samples per microsecond = 8 samples per 0.5us chip.
    -- This value propagates through the entire pipeline — changing it
    -- adapts all correlator windows, decoder timing, and buffer sizes.
    constant SPS : positive := 8;

    -- Chips (half-bit periods) per data bit. Mode-S PPM encodes each bit
    -- as two chips: '1' = [high, low], '0' = [low, high]. So there are
    -- always 2 chips per bit, giving SPS*SPB = 16 samples per data bit.
    constant SPB : positive := 2;

    -- =========================================================================
    -- Decoder Configuration
    -- =========================================================================

    -- Number of parallel message decoders ("Gold Mode"). When a preamble is
    -- detected, the preamble detector assigns it to an available decoder.
    -- Multiple decoders allow the receiver to handle overlapping packets —
    -- critical in busy airspace near airports. 8 decoders can track 8
    -- simultaneous transmissions. Reduce to 4 if targeting the smaller
    -- Zynq-7010 FPGA.
    constant NUM_DECODERS : positive := 8;

    -- Number of weakest bits to try flipping for error correction.
    -- The bit_flipper module identifies the N least-confident bits (by
    -- soft-decision score) and brute-force tries all 2^N combinations,
    -- checking CRC after each. With N=5, that's 32 CRC checks per message.
    -- This is the technique that gives commercial-grade decode performance.
    constant NUM_WEAK_BITS : positive := 5;

    -- Number of recently seen ICAO addresses retained for validating
    -- Address/Parity replies such as DF4/DF5.
    constant ICAO_CACHE_DEPTH : positive := 64;

    -- =========================================================================
    -- Signal Widths
    -- =========================================================================

    -- Width of the power/magnitude signal path. After computing I^2 + Q^2
    -- from 12-bit ADC samples, the result needs sufficient dynamic range.
    -- 24 bits supports the full range of squared 12-bit values with headroom
    -- for accumulation in the correlator.
    constant INPUT_POWER_WIDTH : positive := 24;

    -- Width of the free-running timestamp counter. 64 bits at 100 MHz gives
    -- a rollover period of 2^64 / 100e6 ≈ 5,849 years — effectively infinite.
    -- The counter value is latched on preamble detection (Time-Of-Arrival)
    -- and on PPS edges, allowing Linux to reconstruct UTC timestamps.
    constant COUNTER_WIDTH : positive := 64;

    -- =========================================================================
    -- Preamble Timing (derived from SPS)
    -- =========================================================================
    -- The Mode-S preamble is 8 microseconds long, containing 4 pulses at
    -- positions 0, 1, 3.5, and 4.5 microseconds. At 1 MHz chip rate that's
    -- 16 chips, and at SPS=8 that's 128 samples. The preamble detector uses
    -- a sliding window across this buffer to correlate against the expected
    -- pulse pattern.
    -- =========================================================================

    constant PREAMBLE_BITS          : positive := 8;
    constant PREAMBLE_BUFFER_LENGTH : positive := SPS * SPB * PREAMBLE_BITS;

    -- =========================================================================
    -- Types
    -- =========================================================================

    -- Array of raw 112-bit decoded messages (used by multi-decoder output bus).
    -- Each element is a complete Mode-S frame: 5-bit DF + 3-bit CA + 24-bit
    -- ICAO address + 56-bit message/data + 24-bit CRC = 112 bits total.
    type messages_t is array(natural range <>) of std_logic_vector(111 downto 0);

    -- Array of timestamps (one per decoder), for TOA passthrough.
    type toas_t is array(natural range <>) of unsigned(COUNTER_WIDTH-1 downto 0);

    -- Array of RPL values (one per decoder), for signal level passthrough.
    type rpls_t is array(natural range <>) of signed(INPUT_POWER_WIDTH-1 downto 0);

    -- Complete decoded message with metadata for MLAT timestamping.
    -- This is the output record delivered to the Linux PS (Processing System):
    type adsb_message_t is record
        data        : std_logic_vector(111 downto 0);           -- 14-byte Mode-S frame
        timestamp   : unsigned(COUNTER_WIDTH-1 downto 0);       -- raw counter at Time-Of-Arrival
        fractional  : unsigned(15 downto 0);                    -- sub-sample timing offset from correlator peak interpolation
        rpl         : signed(INPUT_POWER_WIDTH-1 downto 0);     -- Reference Power Level (signal strength)
        valid       : std_logic;                                -- message is valid (CRC passed)
    end record;

    -- Array of timestamped messages (one per decoder instance).
    type adsb_messages_t is array(natural range <>) of adsb_message_t;

    -- =========================================================================
    -- Message Timing
    -- =========================================================================

    -- Maximum number of samples for a full extended message:
    -- 112 bits × SPS samples/chip × SPB chips/bit = 1792 samples (112 µs)
    -- Add 25% margin for filter group delay and edge detection latency.
    -- Used by both message_decoder (sample gating) and preamble_detector
    -- (detection holdoff after claiming a decoder).
    constant EXTENDED_MESSAGE_LENGTH : integer := (112 * SPS * SPB * 5) / 4;  -- 2240

    -- Default preamble detection holdoff in samples.
    -- After a preamble peak fires, detection is suppressed for this many
    -- samples to prevent payload re-triggers from claiming additional
    -- decoders. 512 samples (32 µs) was empirically determined to give
    -- the best throughput (~15% more accepted frames than 2240) while
    -- keeping the drop rate manageable via PS-side filtering.
    -- The PS can override this at runtime via the AXI config register.
    constant PREAMBLE_HOLDOFF_DEFAULT : integer := 32 * SPS * SPB;  -- 512 samples = 32 µs

    -- =========================================================================
    -- Detection Thresholds
    -- =========================================================================

    -- Minimum power level for a sample to be considered "signal present"
    -- during preamble detection. Samples below this are treated as noise.
    -- The current Pluto vendor ingress produces a much smaller |I|+|Q| scalar
    -- than the original decoder assumptions, so keep this low for live
    -- bring-up until thresholds are derived from measured captures.
    -- Raised from 500 to 2000 for the direct-ADC path (FIR bypass).
    -- POWER_MAX ~8190 on live signals, so 2000 filters noise while still
    -- catching real preamble pulses.
    constant POWER_THRESHOLD : signed(INPUT_POWER_WIDTH-1 downto 0) :=
        to_signed(2000, INPUT_POWER_WIDTH);

    -- Lower threshold used by the edge detector. A rising edge is only
    -- recognised if surrounding samples exceed this minimum, preventing
    -- noise spikes from triggering false edge detections.
    constant EDGE_POWER_THRESHOLD : signed(INPUT_POWER_WIDTH-1 downto 0) :=
        to_signed(100, INPUT_POWER_WIDTH);

    -- =========================================================================
    -- Preamble Detector Ratio Thresholds
    -- =========================================================================
    -- These control the preamble quality gate. All are expressed as shift
    -- amounts (powers of two) for zero-cost hardware implementation.
    --
    -- Original values were designed for a clean, unfiltered 16 MHz waveform.
    -- The FIR-decimated |I|+|Q| ingress path smears pulse/gap contrast,
    -- requiring relaxation for real hardware bring-up.

    -- Quiet zone ratio: each quiet zone sum must be less than the adjacent
    -- pulse sum right-shifted by this amount.
    -- 0 = quiet < pulse, 1 = quiet < pulse/2, 2 = quiet < pulse/4.
    -- Relaxed one step further after re-enabling the quiet gate on the
    -- direct-ADC path showed that pulse/gap contrast is still weaker than
    -- the original detector expected.
    constant QUIET_ZONE_RATIO_SHIFT : natural := 0;

    -- Quiet-score ratio: the summed per-zone pulse-minus-gap contrast must
    -- exceed total pulse energy right-shifted by this amount.
    -- 1 = quiet_score > sum_pulse/2, 2 = quiet_score > sum_pulse/4.
    -- Start conservatively and tune later from live captures.
    constant QUIET_SCORE_SHIFT : natural := 1;

    -- Aggregate SNR ratio: total pulse energy must exceed total gap energy
    -- left-shifted by this amount.
    -- 0 = pulse > gap, 1 = pulse > gap*2, 2 = pulse > gap*4.
    -- Relaxed again after live counter capture showed the main candidate loss
    -- is at the SNR gate rather than the quiet-zone checks.
    constant SNR_RATIO_SHIFT : natural := 0;

    -- =========================================================================
    -- BSD Classifier Thresholds
    -- =========================================================================
    -- The BSD calculator classifies each sample relative to RPL:
    --   typeA ("signal present"): sample is within [rpl_low, rpl_high]
    --   typeB ("quiet/noise"):    sample is below rpl_lowlow
    --   neither:                  ambiguous, ignored in scoring
    --
    -- Thresholds are computed from RPL using shifts and adds (no multiplier).
    -- The FIR-decimated |I|+|Q| waveform has more amplitude spread than the
    -- original simulation assumed, so bring-up values are wider/more permissive.

    -- typeA lower bound = RPL - RPL/2 = RPL × 0.5
    -- Original: RPL - (RPL/2 + RPL/4) = RPL × 0.25
    -- Relaxed for bring-up: wider acceptance window for signal-present samples.
    constant BSD_RPL_LOW_SUB_SHIFT : natural := 1;   -- subtract RPL >> this from RPL

    -- typeA upper bound = RPL + RPL + RPL/2 = RPL × 2.5
    -- Original: RPL + RPL/2 + RPL/4 = RPL × 1.75
    -- Relaxed for bring-up: accept samples up to 2.5× RPL as signal-present.
    constant BSD_RPL_HIGH_ADD1_SHIFT : natural := 0;  -- add RPL >> this  (RPL × 1.0)
    constant BSD_RPL_HIGH_ADD2_SHIFT : natural := 1;  -- add RPL >> this  (RPL × 0.5)

    -- typeB threshold = rpl_low / 8
    -- Original: rpl_low / 2 = RPL × 0.125 (with old rpl_low = 0.25 × RPL)
    -- Bring-up: rpl_low / 8 = RPL × 0.0625 (with new rpl_low = 0.5 × RPL)
    -- Lower typeB boundary so fewer samples fall into the noise
    -- classification, reducing false-quiet scoring on a smeared waveform.
    constant BSD_RPL_LOWLOW_SHIFT : natural := 3;     -- shift rpl_low right by this

end package;
