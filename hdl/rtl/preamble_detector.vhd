-- =============================================================================
-- preamble_detector.vhd — Mode-S Preamble Correlator with Timestamp Capture
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with timestamp (TOA) capture added for
-- MLAT support. Detection algorithm inspired by dump1090's correlation
-- approach for improved sensitivity to weak signals.
--
-- Every Mode-S transmission begins with an 8-microsecond preamble containing
-- four pulses at specific positions:
--
--   Pulse 0:  0.0 µs  (sample index  0 × SPS =  0)
--   Pulse 1:  1.0 µs  (sample index  2 × SPS = 16)
--   Pulse 2:  3.5 µs  (sample index  7 × SPS = 56)
--   Pulse 3:  4.5 µs  (sample index  9 × SPS = 72)
--
-- The detector uses a sliding-window correlation approach:
--   1. A 128-sample shift register (power_grid) buffers the incoming power
--      signal, along with a parallel edge flag register (edge_grid).
--
--   2. Four accumulators (sum0-sum3) maintain running sums of power over
--      a SAMPLE_WINDOW (5 samples) at each expected pulse position, plus
--      four quiet-zone accumulators between/after the pulses.
--      These are updated incrementally: add the new sample entering the
--      window, subtract the one leaving.
--
--   3. Detection criteria (unified quality gate, all must pass):
--      a. All four pulse sums exceed POWER_THRESHOLD (absolute minimum)
--      b. The summed per-zone pulse-minus-gap contrast exceeds a scaled
--         fraction of total pulse energy (quiet-score gate)
--      c. Total pulse energy exceeds a scaled multiple of total gap energy
--         (aggregate SNR gate)
--      d. Total gap energy non-negative (guards against overflow artefacts)
--      Edge detection flags are captured for diagnostics but do not gate
--      detection. Weak signals (2 of 4 edges or fewer) are intentionally
--      accepted — the quality gate alone is sufficient for filtering.
--
--   4. Duplicate suppression (peak detection + message-length holdoff):
--      Adjacent sliding-window positions often pass the quality gate for
--      the same real preamble, and the message payload itself can create
--      false peaks as data flows through the correlator buffer. Two
--      mechanisms prevent a single packet from saturating all decoders:
--
--      a. Peak detection: the detector tracks RPL across consecutive
--         qualifying cycles and only fires on the local maximum (when
--         RPL starts declining). This collapses a cluster of adjacent
--         qualifying positions into one claim.
--
--      b. Configurable holdoff: after a peak fires, detection is
--         suppressed for holdoff_cfg samples (default 512 = 32 µs).
--         This covers preamble + short-frame payload, preventing
--         payload-induced re-triggers. The holdoff is runtime-tunable
--         via AXI to allow A/B testing of different values.
--
--   5. The Reference Power Level (RPL) is estimated as the average of the
--      peak power in each of the four pulse windows: (max0+max1+max2+max3)/4.
--
--   6. On detection, the current timestamp counter value is latched as the
--      Time-Of-Arrival (TOA) for MLAT processing.
--
-- Decoder assignment:
--   The module manages a pool of NUM_MESSAGE_DECODER parallel decoders.
--   When a preamble is detected, it scans for the first non-busy decoder
--   and assigns the message to it (first-free strategy). A short pending
--   delay (2 cycles) compensates for the peak detector's 1-cycle latency,
--   preserving the original sample alignment between detection and SOM.
-- =============================================================================

library ieee;
    use ieee.numeric_std.all;
    use ieee.std_logic_1164.all;

library work ;
    use work.adsb_pkg.all ;

entity preamble_detector is
  generic(
    NUM_MESSAGE_DECODER : integer := 1;      -- Number of downstream decoder instances
    MESSAGE_DELAY_G     : integer := 50;
    OUTPUT_TAP_G        : integer := 76;
    ENABLE_DEEP_DEBUG   : boolean := false
  );
  port(
    clock           :   in  std_logic;                                      -- System clock
    reset           :   in  std_logic;                                      -- Asynchronous reset

    power_in        :   in  signed(INPUT_POWER_WIDTH-1 downto 0);           -- Power sample from edge detector
    edge_in         :   in  std_logic;                                      -- Edge flag from edge detector
    in_valid        :   in  std_logic;                                      -- Input valid strobe

    decoder_busy    :   in  std_logic_vector(NUM_MESSAGE_DECODER-1 downto 0);  -- Busy flags from all decoders

    counter_value   :   in  unsigned(COUNTER_WIDTH-1 downto 0);             -- Current timestamp counter (for TOA)
    quiet_score_shift_cfg : in unsigned(2 downto 0);
    snr_ratio_shift_cfg   : in unsigned(2 downto 0);
    holdoff_cfg           : in unsigned(11 downto 0);              -- Runtime holdoff in samples (0-4095)

    power_out       :   out signed(INPUT_POWER_WIDTH-1 downto 0);           -- Power output to decoders
    out_valid       :   out std_logic;                                      -- Output valid strobe
    som             :   out std_logic_vector( NUM_MESSAGE_DECODER-1 downto 0);  -- Start-Of-Message per decoder
    rpl             :   out signed(INPUT_POWER_WIDTH-1 downto 0);           -- Reference Power Level
    toa_out         :   out unsigned(COUNTER_WIDTH-1 downto 0);             -- Time-Of-Arrival timestamp
    debug_pass_count :  out unsigned(31 downto 0);
    debug_detect_count : out unsigned(31 downto 0);
    debug_abs_gate_count : out unsigned(31 downto 0);
    debug_quiet_gate_count : out unsigned(31 downto 0);
    debug_quiet_a_fail_count : out unsigned(31 downto 0);
    debug_quiet_b_fail_count : out unsigned(31 downto 0);
    debug_quiet_c_fail_count : out unsigned(31 downto 0);
    debug_quiet_d_fail_count : out unsigned(31 downto 0);
    debug_snr_gate_count : out unsigned(31 downto 0);
    debug_holdoff_count : out unsigned(31 downto 0);
    debug_peak_age : out unsigned(31 downto 0);
    debug_no_free_count : out unsigned(31 downto 0);
    debug_busy_drop_count : out unsigned(31 downto 0)
  );
end entity;


architecture arch of preamble_detector is

    -- Number of additional RPL checks after initial detection (not currently used
    -- in the main detection path, but reserved for future double-check logic).
    constant RPL_DOUBLECHECK        : integer := 3;

    -- Preamble is 8 bits (8 µs) long
    constant PREAMBLE_LENGTH        : integer := 8;

    -- Total buffer length in samples: SPS × SPB × 8 = 8 × 2 × 8 = 128 samples.
    -- This holds one full preamble's worth of signal history.
    constant LOCAL_PREAMBLE_BUFFER_LENGTH : integer := SPS*SPB*PREAMBLE_LENGTH;

    -- After the 4th preamble pulse completes (at ~5 µs), there are 3 µs of
    -- quiet period before the data payload begins. At 16 MSPS, that's 48 samples.
    -- We delay the SOM signal by this many samples so it fires at the exact
    -- start of the first data bit.
    constant MESSAGE_DELAY          : integer := MESSAGE_DELAY_G;

    -- Width of the sliding sum window (samples). Each pulse position accumulates
    -- 5 consecutive samples of power for a more robust detection than a single sample.
    constant SAMPLE_WINDOW          : integer := 5;

    -- Pulse position indices for the 4 preamble pulses.
    -- "sum_in" is the index where a new sample enters the accumulation window.
    -- "sum_out" is the index where a sample exits the window.
    -- The running sum is updated as: sum += power[sum_in] - power[sum_out]
    constant sum0_in                : integer := SAMPLE_WINDOW-1;            --  4
    constant sum1_in                : integer := 2*SPS + SAMPLE_WINDOW-1;    -- 20
    constant sum2_in                : integer := 7*SPS + SAMPLE_WINDOW-1;    -- 60
    constant sum3_in                : integer := 9*SPS + SAMPLE_WINDOW-1;    -- 76

    constant sum0_out               : integer := 0;                          --  0
    constant sum1_out               : integer := 2*SPS;                      -- 16
    constant sum2_out               : integer := 7*SPS;                      -- 56
    constant sum3_out               : integer := 9*SPS;                      -- 72

    -- Quiet-zone check indices.
    -- A real preamble has LOW power between pulses. Data content typically
    -- has high power at these positions, so requiring quiet zones rejects
    -- most false triggers. Four zones cover all gaps in the preamble.
    --
    -- Quiet zone A: between pulses 0 and 1 (indices 8-12, gap at 0.5-0.75 us)
    constant quiet_a_out            : integer := 1*SPS;                      --  8
    constant quiet_a_in             : integer := 1*SPS + SAMPLE_WINDOW-1;    -- 12
    --
    -- Quiet zone B: between pulses 1 and 2 (indices 36-40, the big 2.5 us gap)
    constant quiet_b_out            : integer := 4*SPS + 4;                  -- 36
    constant quiet_b_in             : integer := 4*SPS + SAMPLE_WINDOW + 3;  -- 40
    --
    -- Quiet zone C: between pulses 2 and 3 (indices 64-68, gap at 4.0-4.25 us)
    constant quiet_c_out            : integer := 8*SPS;                      -- 64
    constant quiet_c_in             : integer := 8*SPS + SAMPLE_WINDOW-1;    -- 68
    --
    -- Quiet zone D: after pulse 3 (indices 80-84, gap at 5.0-5.25 us)
    constant quiet_d_out            : integer := 10*SPS;                     -- 80
    constant quiet_d_in             : integer := 10*SPS + SAMPLE_WINDOW-1;   -- 84

    -- Output tap: aligned with the last pulse's sum window entry point
    constant OUTPUT_TAP             : integer := OUTPUT_TAP_G;

    -- Edge detection flags at the four pulse positions (registered)
    signal edge_qualifier           : std_logic_vector(3 downto 0);
    signal edge_register            : std_logic_vector(3 downto 0);

    -- Shift registers for power samples and edge flags over the full
    -- preamble buffer (128 samples deep)
    type power_array is array(natural range <>) of signed(INPUT_POWER_WIDTH-1 downto 0);
    signal power_grid               : power_array (LOCAL_PREAMBLE_BUFFER_LENGTH-1 downto 0);
    signal edge_grid                : std_logic_vector(LOCAL_PREAMBLE_BUFFER_LENGTH-1 downto 0);

    -- Internal control signals
    signal register_valid           : std_logic;           -- Delayed valid for registered detection output
    signal qualify_valid            : std_logic;           -- Second pipeline stage valid
    signal preamble_detected        : std_logic;           -- Preamble correlation passed this cycle
    signal register_rpl             : signed(INPUT_POWER_WIDTH-1 downto 0);  -- Registered RPL estimate

    -- Pipelined predicate signals (registered at end of stage 1, used in stage 2)
    signal abs_pass_r               : boolean := false;
    signal quiet_pass_r             : boolean := false;
    signal quiet_a_pass_r           : boolean := false;
    signal quiet_b_pass_r           : boolean := false;
    signal quiet_c_pass_r           : boolean := false;
    signal quiet_d_pass_r           : boolean := false;
    signal snr_pass_r               : boolean := false;
    signal cycle_rpl_r              : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');

    -- Decoder assignment state
    signal rpl_countdown            : integer range 0 to 3;
    signal som_pending              : std_logic_vector(NUM_MESSAGE_DECODER-1 downto 0);

    -- Timestamp latching — captures counter_value when preamble is detected
    signal toa_latched              : unsigned(COUNTER_WIDTH-1 downto 0);
    signal debug_pass_count_i       : unsigned(31 downto 0) := (others => '0');
    signal debug_detect_count_i     : unsigned(31 downto 0) := (others => '0');
    signal debug_abs_gate_count_i   : unsigned(31 downto 0) := (others => '0');
    signal debug_quiet_gate_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_quiet_a_fail_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_quiet_b_fail_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_quiet_c_fail_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_quiet_d_fail_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_snr_gate_count_i   : unsigned(31 downto 0) := (others => '0');
    signal debug_holdoff_count_i    : unsigned(31 downto 0) := (others => '0');
    signal debug_peak_age_i         : unsigned(31 downto 0) := (others => '0');
    signal debug_no_free_count_i    : unsigned(31 downto 0) := (others => '0');
    signal debug_busy_drop_count_i  : unsigned(31 downto 0) := (others => '0');

begin

    -- Output the latched timestamp (held stable until next detection)
    toa_out <= toa_latched;

    -- =========================================================================
    -- Detection process: sliding-window preamble correlation
    --
    -- On each valid input sample:
    --   - Shift power and edge values into the 128-sample buffers
    --   - Update the four running power sums (add new, subtract old)
    --   - Capture edge flags (diagnostic only)
    --   - Reuse the pulse window sums for a low-cost RPL estimate
    --
    -- One cycle later (register_valid):
    --   - Run unified quality gate (sums + quiet zones + SNR)
    --   - Track RPL across consecutive qualifying cycles (peak detection)
    --   - Fire preamble_detected when RPL starts declining (local max)
    --   - Arm message-length holdoff to suppress payload re-triggers
    -- =========================================================================
    detection : process(clock,reset)
        -- Sum accumulators: 3 extra bits beyond INPUT_POWER_WIDTH to hold
        -- the sum of SAMPLE_WINDOW (5) power values without overflow.
        constant SUM_WIDTH : integer := INPUT_POWER_WIDTH + 3;  -- 27 bits
        variable sum0 : signed(SUM_WIDTH-1 downto 0);
        variable sum1 : signed(SUM_WIDTH-1 downto 0);
        variable sum2 : signed(SUM_WIDTH-1 downto 0);
        variable sum3 : signed(SUM_WIDTH-1 downto 0);
        variable sum_quiet_a : signed(SUM_WIDTH-1 downto 0);
        variable sum_quiet_b : signed(SUM_WIDTH-1 downto 0);
        variable sum_quiet_c : signed(SUM_WIDTH-1 downto 0);
        variable sum_quiet_d : signed(SUM_WIDTH-1 downto 0);
        -- Wider accumulators for aggregate SNR check.
        constant SNR_WIDTH : integer := INPUT_POWER_WIDTH + 5;  -- 29 bits
        variable sum_pulse   : signed(SNR_WIDTH-1 downto 0);
        variable sum_gap     : signed(SNR_WIDTH-1 downto 0);
        variable quiet_score : signed(SNR_WIDTH-1 downto 0);
        -- Peak detection + holdoff.
        -- See header comment (section 4) for full rationale.
        -- Holdoff is now runtime-configurable via AXI (holdoff_cfg port).
        variable candidate_active : boolean;
        variable candidate_rpl    : signed(INPUT_POWER_WIDTH-1 downto 0);
        variable candidate_toa    : unsigned(COUNTER_WIDTH-1 downto 0);
        variable candidate_edge   : std_logic_vector(3 downto 0);
        variable candidate_age    : integer range 0 to LOCAL_PREAMBLE_BUFFER_LENGTH;
        variable holdoff          : integer range 0 to 4095;
        variable passes           : boolean;
    begin
        if( reset = '1' ) then
            preamble_detected <= '0';
            register_valid <= '0';
            qualify_valid <= '0';
            abs_pass_r <= false;
            quiet_pass_r <= false;
            quiet_a_pass_r <= false;
            quiet_b_pass_r <= false;
            quiet_c_pass_r <= false;
            quiet_d_pass_r <= false;
            snr_pass_r <= false;
            cycle_rpl_r <= (others => '0');
            sum0 := (others => '0');
            sum1 := (others => '0');
            sum2 := (others => '0');
            sum3 := (others => '0');
            sum_quiet_a := (others => '0');
            sum_quiet_b := (others => '0');
            sum_quiet_c := (others => '0');
            sum_quiet_d := (others => '0');
            power_grid <= (others => (others => '0'));
            edge_grid <= (others => '0');
            toa_latched <= (others => '0');
            candidate_active := false;
            candidate_age := 0;
            holdoff := 0;
            debug_pass_count_i <= (others => '0');
            debug_detect_count_i <= (others => '0');
            debug_abs_gate_count_i <= (others => '0');
            debug_quiet_gate_count_i <= (others => '0');
            debug_snr_gate_count_i <= (others => '0');
            if ENABLE_DEEP_DEBUG then
                debug_quiet_a_fail_count_i <= (others => '0');
                debug_quiet_b_fail_count_i <= (others => '0');
                debug_quiet_c_fail_count_i <= (others => '0');
                debug_quiet_d_fail_count_i <= (others => '0');
                debug_holdoff_count_i <= (others => '0');
                debug_peak_age_i <= (others => '0');
            end if;
        elsif (rising_edge(clock))then
            edge_register <= (others => '0');
            preamble_detected <= '0';

            -- =================================================================
            -- Stage 1: compute detection predicates from registered sums.
            -- Results are registered into signals for use by stage 2.
            -- =================================================================
            if(register_valid = '1') then

                -- Aggregate pulse and gap energy for SNR check
                sum_pulse := resize(sum0, SNR_WIDTH) + resize(sum1, SNR_WIDTH)
                           + resize(sum2, SNR_WIDTH) + resize(sum3, SNR_WIDTH);
                sum_gap   := resize(sum_quiet_a, SNR_WIDTH) + resize(sum_quiet_b, SNR_WIDTH)
                           + resize(sum_quiet_c, SNR_WIDTH) + resize(sum_quiet_d, SNR_WIDTH);

                -- RPL for this cycle (used for peak comparison, not output).
                -- Reuse the pulse sums instead of a per-window max search to
                -- keep the RX-clock critical path shallow during bring-up.
                cycle_rpl_r <= resize(shift_right(sum_pulse, 4), rpl'length);

                abs_pass_r <=
                    (sum0 > resize(POWER_THRESHOLD, SUM_WIDTH)) and
                    (sum1 > resize(POWER_THRESHOLD, SUM_WIDTH)) and
                    (sum2 > resize(POWER_THRESHOLD, SUM_WIDTH)) and
                    (sum3 > resize(POWER_THRESHOLD, SUM_WIDTH));

                quiet_a_pass_r <= (sum_quiet_a < shift_right(sum0, QUIET_ZONE_RATIO_SHIFT));
                quiet_b_pass_r <= (sum_quiet_b < shift_right(sum1, QUIET_ZONE_RATIO_SHIFT));
                quiet_c_pass_r <= (sum_quiet_c < shift_right(sum2, QUIET_ZONE_RATIO_SHIFT));
                quiet_d_pass_r <= (sum_quiet_d < shift_right(sum3, QUIET_ZONE_RATIO_SHIFT));

                quiet_score :=
                    resize(sum0 - sum_quiet_a, SNR_WIDTH) +
                    resize(sum1 - sum_quiet_b, SNR_WIDTH) +
                    resize(sum2 - sum_quiet_c, SNR_WIDTH) +
                    resize(sum3 - sum_quiet_d, SNR_WIDTH);
                quiet_pass_r <=
                    (quiet_score > shift_right(sum_pulse, to_integer(quiet_score_shift_cfg)));

                snr_pass_r <=
                    (sum_pulse > shift_left(sum_gap, to_integer(snr_ratio_shift_cfg))) and
                    (sum_gap >= to_signed(0, SNR_WIDTH));
            end if;

            -- =================================================================
            -- Stage 2: quality gate, peak detection, counter updates.
            -- Uses registered predicates from stage 1 — much shallower logic.
            -- =================================================================
            if(qualify_valid = '1') then

                passes := abs_pass_r and quiet_pass_r and snr_pass_r;

                if abs_pass_r then
                    debug_abs_gate_count_i <= debug_abs_gate_count_i + 1;
                    if ENABLE_DEEP_DEBUG then
                        if not quiet_a_pass_r then
                            debug_quiet_a_fail_count_i <= debug_quiet_a_fail_count_i + 1;
                        end if;
                        if not quiet_b_pass_r then
                            debug_quiet_b_fail_count_i <= debug_quiet_b_fail_count_i + 1;
                        end if;
                        if not quiet_c_pass_r then
                            debug_quiet_c_fail_count_i <= debug_quiet_c_fail_count_i + 1;
                        end if;
                        if not quiet_d_pass_r then
                            debug_quiet_d_fail_count_i <= debug_quiet_d_fail_count_i + 1;
                        end if;
                    end if;
                end if;
                if abs_pass_r and quiet_pass_r then
                    debug_quiet_gate_count_i <= debug_quiet_gate_count_i + 1;
                end if;
                if abs_pass_r and quiet_pass_r and snr_pass_r then
                    debug_snr_gate_count_i <= debug_snr_gate_count_i + 1;
                end if;

                -- Suppress during refractory holdoff after a peak claim
                if holdoff > 0 then
                    holdoff := holdoff - 1;
                    if ENABLE_DEEP_DEBUG and passes then
                        debug_holdoff_count_i <= debug_holdoff_count_i + 1;
                    end if;
                    passes := false;
                end if;

                if passes then
                    debug_pass_count_i <= debug_pass_count_i + 1;
                end if;

                -- Peak detection: fire on the first decline while the gate
                -- still passes. This avoids adding payload-content-dependent
                -- delay to the eventual SOM alignment.
                if passes then
                    if candidate_active and candidate_age < LOCAL_PREAMBLE_BUFFER_LENGTH then
                        candidate_age := candidate_age + 1;
                    end if;

                    if not candidate_active then
                        -- New candidate
                        candidate_active := true;
                        candidate_rpl := cycle_rpl_r;
                        candidate_toa := counter_value;
                        candidate_edge := edge_qualifier;
                        candidate_age := 0;
                    elsif cycle_rpl_r > candidate_rpl then
                        -- Improved candidate, keep tracking the peak
                        candidate_rpl := cycle_rpl_r;
                        candidate_toa := counter_value;
                        candidate_edge := edge_qualifier;
                    else
                        -- Peak found while we still pass the quality gate
                        preamble_detected <= '1';
                        debug_detect_count_i <= debug_detect_count_i + 1;
                        register_rpl <= candidate_rpl;
                        toa_latched <= candidate_toa;
                        edge_register <= candidate_edge;
                        if ENABLE_DEEP_DEBUG then
                            debug_peak_age_i <= to_unsigned(candidate_age, debug_peak_age_i'length);
                        end if;
                        candidate_active := false;
                        candidate_age := 0;
                        holdoff := to_integer(holdoff_cfg);
                        -- synthesis translate_off
                        report "PREAMBLE_DBG: *** PREAMBLE DETECTED (decline) " &
                               "rpl=" & integer'image(to_integer(candidate_rpl)) &
                               " age=" & integer'image(candidate_age) &
                               " edge_qual=" & integer'image(to_integer(unsigned(candidate_edge)));
                        -- synthesis translate_on
                    end if;
                else
                    -- Detection stopped or holdoff: fire pending peak
                    if candidate_active then
                        preamble_detected <= '1';
                        debug_detect_count_i <= debug_detect_count_i + 1;
                        register_rpl <= candidate_rpl;
                        toa_latched <= candidate_toa;
                        edge_register <= candidate_edge;
                        if ENABLE_DEEP_DEBUG then
                            debug_peak_age_i <= to_unsigned(candidate_age, debug_peak_age_i'length);
                        end if;
                        candidate_active := false;
                        candidate_age := 0;
                        holdoff := to_integer(holdoff_cfg);
                        -- synthesis translate_off
                        report "PREAMBLE_DBG: *** PREAMBLE DETECTED (peak) " &
                               "rpl=" & integer'image(to_integer(candidate_rpl)) &
                               " age=" & integer'image(candidate_age) &
                               " edge_qual=" & integer'image(to_integer(unsigned(candidate_edge)));
                        -- synthesis translate_on
                    end if;
                end if;
            end if;

            if(in_valid = '1') then
                -- Shift new samples into the 128-deep buffers (newest at index N-1,
                -- oldest at index 0). The buffer slides forward by one sample each cycle.
                power_grid <= power_in & power_grid(power_grid'length-1 downto 1);
                edge_grid <= edge_in & edge_grid(edge_grid'length-1 downto 1);

                -- Update the running sums incrementally:
                -- Add the sample entering the window, subtract the one leaving.
                -- This is O(1) per cycle instead of O(SAMPLE_WINDOW).
                -- Resize power samples to SUM_WIDTH before arithmetic to avoid overflow.
                -- Pulse sums (must be HIGH for detection):
                sum0 := sum0 + resize(power_grid(sum0_in), SUM_WIDTH) - resize(power_grid(sum0_out), SUM_WIDTH);
                sum1 := sum1 + resize(power_grid(sum1_in), SUM_WIDTH) - resize(power_grid(sum1_out), SUM_WIDTH);
                sum2 := sum2 + resize(power_grid(sum2_in), SUM_WIDTH) - resize(power_grid(sum2_out), SUM_WIDTH);
                sum3 := sum3 + resize(power_grid(sum3_in), SUM_WIDTH) - resize(power_grid(sum3_out), SUM_WIDTH);
                -- Quiet zone sums (must be LOW for detection):
                sum_quiet_a := sum_quiet_a + resize(power_grid(quiet_a_in), SUM_WIDTH) - resize(power_grid(quiet_a_out), SUM_WIDTH);
                sum_quiet_b := sum_quiet_b + resize(power_grid(quiet_b_in), SUM_WIDTH) - resize(power_grid(quiet_b_out), SUM_WIDTH);
                sum_quiet_c := sum_quiet_c + resize(power_grid(quiet_c_in), SUM_WIDTH) - resize(power_grid(quiet_c_out), SUM_WIDTH);
                sum_quiet_d := sum_quiet_d + resize(power_grid(quiet_d_in), SUM_WIDTH) - resize(power_grid(quiet_d_out), SUM_WIDTH);

                -- Capture edge flags at each pulse's starting position
                -- (retained for diagnostics, no longer used for detection gating)
                edge_qualifier(0) <= edge_grid(sum0_out);
                edge_qualifier(1) <= edge_grid(sum1_out);
                edge_qualifier(2) <= edge_grid(sum2_out);
                edge_qualifier(3) <= edge_grid(sum3_out);

            end if;

            qualify_valid <= register_valid;
            register_valid <= in_valid;
        end if;
    end process;

    -- =========================================================================
    -- Detection counter (diagnostic only)
    --
    -- Counts total preamble detections. Useful during simulation and debug
    -- to verify the detector is firing. Not exposed as an output port.
    -- =========================================================================
    count_detections : process(clock, reset)
        variable detections : integer := 0 ;
    begin
        if( reset = '1' ) then
            detections := 0 ;
        elsif( rising_edge(clock) ) then
            if( preamble_detected = '1' ) then
                detections := detections + 1 ;
            end if ;
        end if ;
    end process ;

    -- =========================================================================
    -- Decoder assignment and SOM generation (first-free)
    --
    -- When a preamble is detected, this process scans for the first non-busy
    -- decoder and assigns the message to it. The assignment is "pending" for
    -- a short delay to allow RPL refinement when multiple preamble correlations
    -- fire on the same signal.
    --
    -- After the delay:
    --   - If the target decoder is still free → assert SOM for that decoder
    --   - If the target decoder became busy → drop the detection
    --
    -- The RPL comparison (register_rpl > current_rpl) ensures that if multiple
    -- preambles overlap, the stronger one wins (closer aircraft have priority).
    --
    -- Outputs:
    --   som(i)     — single-cycle pulse to start decoder i
    --   power_out  — power sample stream (from buffer tap at sample 122)
    --   rpl        — current Reference Power Level for the active decoder
    --   out_valid  — valid strobe aligned with power_out
    -- =========================================================================
    clock_out : process(clock, reset)
        variable current_rpl            : signed(INPUT_POWER_WIDTH-1 downto 0);
        variable pending_downcount      : integer range 0 to MESSAGE_DELAY;
        variable pending_index          : integer range 0 to NUM_MESSAGE_DECODER-1;
        variable has_pending            : boolean;
        variable ignored                : integer ;   -- Count of dropped detections (busy decoder)
        variable no_free                : integer ;   -- Count of preambles with no free decoder
    begin
        if( reset = '1') then
            som <= (others => '0');
            current_rpl := to_signed(0,INPUT_POWER_WIDTH);
            som_pending <= (others => '0');
            has_pending := false;
            pending_index := 0;
            ignored := 0 ;
            no_free := 0 ;
            if ENABLE_DEEP_DEBUG then
                debug_no_free_count_i <= (others => '0');
                debug_busy_drop_count_i <= (others => '0');
            end if;
        elsif (rising_edge(clock)) then
            -- Default: no SOM pulses this cycle
            som <= (others => '0');
            out_valid <= qualify_valid;
            power_out <= power_grid(OUTPUT_TAP);  -- Output tap aligned to detector timing
            rpl <= current_rpl;

            -- Pending SOM countdown and detection capture run every cycle,
            -- not gated by qualify_valid. preamble_detected is a signal set
            -- in the detection process during qualify_valid — it is only
            -- visible here on the NEXT clock edge, when qualify_valid is
            -- already low. Gating this by qualify_valid would miss every
            -- detection.

            -- If we have a pending detection, count down the delay.
            -- Decrement on qualify_valid only so the count tracks sample
            -- periods, not raw clock cycles.
            if has_pending then
                if pending_downcount > 0 then
                    if qualify_valid = '1' then
                        pending_downcount := pending_downcount - 1;
                    end if;
                else
                    -- Delay expired — try to start the decoder
                    if( decoder_busy(pending_index) = '0' ) then
                        -- Decoder is free: assert SOM
                        som(pending_index) <= '1';
                        som_pending(pending_index) <= '0';
                        current_rpl := to_signed(0,current_rpl'length);
                        has_pending := false;
                        -- synthesis translate_off
                        report "SOM_DBG: SOM fired for decoder " &
                               integer'image(pending_index) &
                               " rpl=" & integer'image(to_integer(rpl));
                        -- synthesis translate_on
                    else
                        -- Decoder became busy — drop this detection
                        som_pending(pending_index) <= '0' ;
                        current_rpl := (others =>'0') ;
                        has_pending := false;
                        ignored := ignored + 1 ;
                        if ENABLE_DEEP_DEBUG then
                            debug_busy_drop_count_i <= debug_busy_drop_count_i + 1;
                        end if;
                    end if ;
                end if;
            end if;

            -- If a new preamble is detected with stronger signal than current,
            -- find the first free decoder and claim it
            if ( preamble_detected = '1') then
                if(register_rpl > current_rpl) then
                    -- Clear old pending if replacing with stronger signal
                    if has_pending then
                        som_pending(pending_index) <= '0';
                        has_pending := false;
                    end if;
                    -- Scan for first non-busy decoder
                    for i in 0 to NUM_MESSAGE_DECODER-1 loop
                        if decoder_busy(i) = '0' then
                            current_rpl := register_rpl;
                            pending_downcount := MESSAGE_DELAY;
                            pending_index := i;
                            has_pending := true;
                            som_pending(i) <= '1';
                            exit ;
                        end if ;
                    end loop ;
                    -- If no free decoder found, detection is lost
                    if not has_pending then
                        no_free := no_free + 1 ;
                        if ENABLE_DEEP_DEBUG then
                            debug_no_free_count_i <= debug_no_free_count_i + 1;
                        end if;
                    end if ;
                end if;
            end if;
        end if;
    end process;

    debug_pass_count <= debug_pass_count_i;
    debug_detect_count <= debug_detect_count_i;
    debug_abs_gate_count <= debug_abs_gate_count_i;
    debug_quiet_gate_count <= debug_quiet_gate_count_i;
    debug_quiet_a_fail_count <= debug_quiet_a_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_quiet_b_fail_count <= debug_quiet_b_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_quiet_c_fail_count <= debug_quiet_c_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_quiet_d_fail_count <= debug_quiet_d_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_snr_gate_count <= debug_snr_gate_count_i;
    debug_holdoff_count <= debug_holdoff_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_peak_age <= debug_peak_age_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_no_free_count <= debug_no_free_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_busy_drop_count <= debug_busy_drop_count_i when ENABLE_DEEP_DEBUG else (others => '0');

end architecture;
