-- =============================================================================
-- adsb_edge_detector.vhd — Leading Edge Detector for Mode-S Signals
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
--
-- This module detects rising edges in the power signal (I²+Q²). Rising edges
-- mark the transitions in Mode-S PPM encoding — each "pulse" in the preamble
-- or data bit begins with a rising edge. The downstream preamble_detector
-- correlates these edges against the expected preamble pulse pattern.
--
-- How it works:
--   1. A shift register (power_grid) holds the most recent SPS+1 = 9 power
--      samples, forming a sliding window over the signal.
--
--   2. A "center tap" sample is compared against its immediate neighbours:
--      if center > previous AND center < next, a rising edge is occurring
--      (the signal is monotonically increasing through the center point).
--
--   3. Additionally, a threshold qualification check ensures that enough
--      samples in the window exceed EDGE_POWER_THRESHOLD (from adsb_pkg).
--      This prevents noise spikes from triggering false edges. The threshold
--      check uses a pattern-matching approach: the last few samples in the
--      window are converted to a bitmask (exceeds/doesn't exceed threshold),
--      and specific bit patterns that correspond to valid edge shapes are
--      accepted. Only the pattern where all qualifying bits are set (value 31
--      = 5'b11111) gets the maximum exceeds_count of 5, which is required
--      for edge detection.
--
--   4. The output is delayed through a valid pipeline to align edge_out
--      with the corresponding power_out sample from the start of the window
--      (power_grid(0)). This ensures downstream modules receive time-aligned
--      power and edge signals.
--
-- Timing:
--   Input:  power_in, in_valid (from upstream power calculator)
--   Output: power_out (delayed), edge_out (edge detected at this sample),
--           out_valid (delayed valid)
--
-- The edge detector adds pipeline latency of EDGE_BUFFER_LENGTH cycles,
-- which is compensated by the output tap selection.
-- =============================================================================

library ieee;
    use ieee.numeric_std.all;
    use ieee.std_logic_1164.all;

library work ;
    use work.adsb_pkg.all ;

entity adsb_edge_detector is
  generic (
    ENABLE_DEEP_DEBUG : boolean := false
  );
  port (
    clock       : in std_logic;                                    -- System clock
    reset       : in std_logic;                                    -- Asynchronous reset

    init        : in std_logic;                                    -- Initialisation signal (unused in current implementation)

    power_in    : in signed(INPUT_POWER_WIDTH-1 downto 0);         -- Input power sample (I²+Q²)
    in_valid    : in std_logic;                                    -- Input valid strobe

    power_out   : out signed(INPUT_POWER_WIDTH-1 downto 0);        -- Delayed power output (aligned with edge_out)
    edge_out    : out std_logic;                                   -- Edge detected at this sample position
    out_valid   : out std_logic;                                   -- Output valid strobe
    debug_edge_shape_count : out unsigned(31 downto 0);
    debug_edge_qual_count  : out unsigned(31 downto 0)
  );
end entity;

architecture arch of adsb_edge_detector is

    -- Buffer length is SPS + 1 = 9 samples. The extra sample beyond SPS
    -- provides the "lookahead" needed for the 3-point edge comparison
    -- (previous, center, next).
    constant EDGE_BUFFER_LENGTH : integer := SPS + 1;

    -- Center tap index for the 3-point comparison. Positioned 5 samples
    -- from the end of the buffer, giving room for the "next" sample above
    -- and several "previous" samples below.
    constant CENTER_TAP         : integer := EDGE_BUFFER_LENGTH-5;

    -- Power sample shift register — holds the sliding window of recent samples.
    type power_array is array(natural range <>) of signed(INPUT_POWER_WIDTH-1 downto 0);
    signal power_grid           : power_array (0 to EDGE_BUFFER_LENGTH-1);

    -- Threshold exceedance flags — one bit per sample in the window.
    -- Set to '1' if the corresponding power sample exceeds EDGE_POWER_THRESHOLD.
    signal power_exceeds_thresh : std_logic_vector(0 to EDGE_BUFFER_LENGTH-1);

    -- Edge detection output shift register — delays edge flags to align
    -- with the corresponding power sample at the output tap.
    signal edge_grid            : std_logic_vector(0 to CENTER_TAP-1);

    -- Valid signal delay line — ensures out_valid aligns with the delayed
    -- power and edge outputs. Length is 2x buffer for the two-stage pipeline
    -- (input shifting + edge calculation).
    signal valid_delay          : std_logic_vector((2*EDGE_BUFFER_LENGTH)-1 downto 0);
    signal debug_edge_shape_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_edge_qual_count_i  : unsigned(31 downto 0) := (others => '0');

begin

    shift_input : process(clock,reset)
        variable tmp_power      : integer;
        variable p_center       : signed(INPUT_POWER_WIDTH-1 downto 0);   -- Power at center tap
        variable p_center_m1    : signed(INPUT_POWER_WIDTH-1 downto 0);   -- Power one sample before center
        variable p_center_p1    : signed(INPUT_POWER_WIDTH-1 downto 0);   -- Power one sample after center
        variable calculate_edge : std_logic;                               -- Delayed valid flag for edge calc
        variable exceeds_count  : integer range 0 to 5;                    -- Number of qualifying samples
        variable edge_detected  : std_logic;                               -- Edge detected this cycle
        variable sample_counter : integer := 0;                            -- Debug/diagnostic counter
    begin
        if(reset = '1') then
            calculate_edge := '0';
            exceeds_count := 0;
            edge_detected := '0';
            sample_counter := 0;
            power_grid <= (others => (others => '0'));
            power_exceeds_thresh <= (others => '0');
            edge_grid <= (others => '0');
            valid_delay <= (others => '0');
            power_out <= (others => '0');
            edge_out <= '0';
            out_valid <= '0';
            if ENABLE_DEEP_DEBUG then
                debug_edge_shape_count_i <= (others => '0');
                debug_edge_qual_count_i <= (others => '0');
            end if;
        elsif(rising_edge(clock)) then

            -- Edge detection runs one cycle after the power shift (calculate_edge
            -- is the delayed version of in_valid). This gives the shift register
            -- time to settle before we read from it.
            if(calculate_edge = '1')  then
                sample_counter := sample_counter +1;

                -- 3-point rising edge test:
                -- The center tap sample must be greater than the sample before it
                -- (rising) and less than the sample after it (still rising).
                -- This identifies the inflection point on a rising slope.
                p_center := power_grid(CENTER_TAP-1);
                p_center_m1 := power_grid(CENTER_TAP-2);
                p_center_p1 := power_grid(CENTER_TAP);
                if ENABLE_DEEP_DEBUG and
                   ((p_center >= p_center_m1) and
                    (p_center <= p_center_p1) and
                    ((p_center > p_center_m1) or (p_center < p_center_p1))) then
                    debug_edge_shape_count_i <= debug_edge_shape_count_i + 1;
                end if;

                if ENABLE_DEEP_DEBUG and exceeds_count >= 5 then
                    debug_edge_qual_count_i <= debug_edge_qual_count_i + 1;
                end if;

                if(  (p_center > p_center_m1 ) and
                    (p_center < p_center_p1) and
                    (exceeds_count >= 5)) then

                    -- Valid rising edge: shift a '1' into the edge pipeline
                    edge_grid <= edge_grid(1 to CENTER_TAP-1) & '1';
                    edge_detected := '1';
                else
                    -- No edge: shift a '0' into the edge pipeline
                    edge_grid <= edge_grid(1 to CENTER_TAP-1) & '0';
                    edge_detected := '0';
                end if;
            else
                edge_detected := '0';
            end if;

            if(in_valid = '1') then
                -- Shift new power sample into the window (newest at the high end,
                -- oldest at index 0 — output tap reads from index 0)
                power_grid <= power_grid(1 to power_grid'length-1) & power_in;

                -- Update threshold exceedance flags in parallel with the power shift
                if(power_in > EDGE_POWER_THRESHOLD) then
                    power_exceeds_thresh <= power_exceeds_thresh(1 to power_grid'length-1) & '1';
                else
                    power_exceeds_thresh <= power_exceeds_thresh(1 to power_grid'length-1) & '0';
                end if;

                -- Pattern-match the threshold flags in the upper portion of the window.
                -- Convert the relevant bits to an integer and check against known-good
                -- edge patterns. This is a compact way to require a specific shape of
                -- signal above threshold near the center tap.
                --
                -- The patterns decoded here represent various combinations of 5 bits
                -- where most or all samples exceed the threshold. The key pattern is:
                --   31 (11111) — all 5 samples above threshold → exceeds_count = 5
                -- Other patterns (partial threshold exceedance) get count = 4, which
                -- is insufficient for edge detection (requires >= 5).
                tmp_power := to_integer( unsigned(power_exceeds_thresh(CENTER_TAP to EDGE_BUFFER_LENGTH-1)));
                case (tmp_power) is
                    when 30 | 35 | 33 | 27 | 17 => exceeds_count := 4;
                    when 31=> exceeds_count := 5;
                    when others => exceeds_count := 0;
                end case;
            end if;

            -- Shift the valid delay line only when a new sample enters the
            -- detector. power_grid advances on in_valid, so the valid token
            -- must advance on the same events; otherwise the output timing
            -- becomes dependent on the raw clock/sample-valid ratio.
            if in_valid = '1' then
                valid_delay <= in_valid & valid_delay(valid_delay'length-1 downto 1);
                out_valid <= valid_delay(0);
            else
                out_valid <= '0';
            end if;

            -- Register outputs: read from the oldest end of each pipeline so
            -- power_out and edge_out stay aligned to the current delayed sample.
            power_out <= power_grid(0);
            edge_out <= edge_grid(0);

            -- Arm edge calculation for next cycle (one-cycle delay after in_valid)
            calculate_edge := in_valid;
        end if;
    end process;

    debug_edge_shape_count <= debug_edge_shape_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_edge_qual_count <= debug_edge_qual_count_i when ENABLE_DEEP_DEBUG else (others => '0');

end architecture;
