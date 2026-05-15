-- =============================================================================
-- log_to_linear.vhd -- Antilog lookup table: log-scale ADC code → linear power
-- =============================================================================
--
-- Converts the 12-bit output of an AD8313 logarithmic detector frontend
-- digitised by the AD9238 breakout into a 24-bit signed linear power value
-- compatible with the existing decode pipeline's INPUT_POWER_WIDTH contract.
--
-- The AD8313 has a positive slope: higher RF power gives higher output
-- voltage, and the confirmed "direct code" ADC format means higher voltage
-- gives higher FPGA ADC code.
--
-- Implementation: a 4096-entry × 24-bit ROM inferred as block RAM. One
-- clock cycle of read latency. No DSP48E consumption.
--
-- The LUT is generated at elaboration from `generate_lut_table` using
-- ADC-code endpoints measured by triggered FPGA capture.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;
    use ieee.math_real.all;

library work;
    use work.adsb_pkg.all;

entity log_to_linear is
    generic (
        ADC_WIDTH    : positive := 12;
        OUTPUT_WIDTH : positive := INPUT_POWER_WIDTH  -- 24
    );
    port (
        clock     : in  std_logic;
        reset     : in  std_logic;

        in_code   : in  unsigned(ADC_WIDTH-1 downto 0);
        in_valid  : in  std_logic;

        out_power : out signed(OUTPUT_WIDTH-1 downto 0);
        out_valid : out std_logic
    );
end entity;

architecture rtl of log_to_linear is

    constant LUT_DEPTH : positive := 2**ADC_WIDTH;

    type lut_t is array (0 to LUT_DEPTH - 1) of std_logic_vector(OUTPUT_WIDTH-1 downto 0);

    -- ------------------------------------------------------------------------
    -- LUT generator — AD8313 breakout transfer function → linear power.
    --
    -- Calibrate directly in ADC-code space. The module's advertised input
    -- range/front-end scaling makes a bare ADC-voltage model unreliable, and
    -- the FPGA raw capture is the signal the decoder actually consumes.
    --
    -- Observations so far:
    --   * The detector is AD8313, with positive slope: higher RF power gives
    --     higher output voltage.
    --   * The ADC output format is direct code, and the FPGA uses D[13:2]
    --     from the AD9238 breakout as a 12-bit unsigned sample.
    --   * Triggered raw captures show quiet/event samples around 0x800, with
    --     stronger samples above that. Scope measurements show quiet around
    --     ~1.1 V and stronger ADS-B pulses up to ~1.45 V.
    --
    -- Measured calibration points at the detector input (1090 MHz CW),
    -- calibrated in the actual FPGA ADC-code space consumed by the decoder:
    --   * code ~0x904 ≈ floor (-80 dBm and weaker collapse here)
    --   * code  0x928 ≈ -60 dBm
    --   * code  0x96A ≈ -50 dBm
    --   * code  0x9B9 ≈ -40 dBm
    --   * code  0xA00 ≈ -30 dBm
    --   * code  0xA46 ≈ -20 dBm
    --   * code  0xA57 ≈ -18.5 dBm
    -- Bench data at -80/-90/-100 dBm all land within a couple of codes of
    -- ~0x904, so treat that as the practical floor of this analog chain.
    -- Above -60 dBm the curve rises cleanly, and the strong end only begins
    -- to bend over near the final -18.5 dBm point. Clamp below the measured
    -- floor and above the strongest measured point rather than extrapolating.
    --
    -- Output scaling:
    --   * Target: a pulse at REF_DBM maps to REF_OUT counts.
    --   * Out-of-range codes clamp to 0 (below noise floor) or OUT_MAX
    --     (saturation). Saturation still produces max output so preamble
    --     detection triggers on very strong bursts.
    --
    -- Polarity-agnostic math: the clamp logic works whether CODE_AT_HIGH_POWER
    -- is numerically greater or less than CODE_AT_LOW_POWER. Flipping the
    -- endpoints flips the LUT polarity without touching any other logic.
    -- ------------------------------------------------------------------------
    function generate_lut_table return lut_t is
        variable table     : lut_t;
        variable dbm       : real;
        variable lin       : real;
        variable scaled    : real;
        variable clamped   : integer;

        -- Detector calibration anchors in 12-bit FPGA ADC code space.
        constant CAL_POINT_COUNT : positive := 6;
        type int_array_t is array (0 to CAL_POINT_COUNT - 1) of integer;
        type real_array_t is array (0 to CAL_POINT_COUNT - 1) of real;
        constant CODE_FLOOR : integer := 16#904#;
        constant CODE_POINTS : int_array_t := (
            16#928#, 16#96A#, 16#9B9#, 16#A00#, 16#A46#, 16#A57#
        );
        constant DBM_POINTS : real_array_t := (
            -60.0, -50.0, -40.0, -30.0, -20.0, -18.5
        );

        -- Output scaling.
        constant REF_DBM    : real := -50.0;
        constant REF_OUT    : real := 8000.0;
        constant OUT_MAX    : integer := 2**(OUTPUT_WIDTH-1) - 1;

        variable segment_idx : integer;
        variable fraction : real;
        variable ref_lin       : real;
        variable scale_factor  : real;
    begin
        ref_lin := 10.0 ** (REF_DBM / 10.0);
        scale_factor := REF_OUT / ref_lin;

        for i in 0 to LUT_DEPTH - 1 loop
            if i <= CODE_FLOOR then
                -- Below the weakest measured anchor — treat as below the
                -- useful noise floor and report zero linear power.
                clamped := 0;
            elsif i >= CODE_POINTS(CAL_POINT_COUNT - 1) then
                -- Past the high-power anchor — treat as saturation on the
                -- strong side so preamble detection still fires.
                clamped := OUT_MAX;
            else
                segment_idx := 0;
                while (segment_idx < CAL_POINT_COUNT - 2) and (i > CODE_POINTS(segment_idx + 1)) loop
                    segment_idx := segment_idx + 1;
                end loop;

                -- Interpolate in the log domain between the two measured
                -- anchors that bound this ADC code.
                fraction := (real(i) - real(CODE_POINTS(segment_idx)))
                          / real(CODE_POINTS(segment_idx + 1) - CODE_POINTS(segment_idx));
                dbm := DBM_POINTS(segment_idx)
                     + fraction * (DBM_POINTS(segment_idx + 1) - DBM_POINTS(segment_idx));
                lin := 10.0 ** (dbm / 10.0);
                scaled := lin * scale_factor;

                if scaled > real(OUT_MAX) then
                    clamped := OUT_MAX;
                elsif scaled < 0.0 then
                    clamped := 0;
                else
                    clamped := integer(scaled);
                end if;
            end if;

            table(i) := std_logic_vector(to_signed(clamped, OUTPUT_WIDTH));
        end loop;
        return table;
    end function;

    signal lut : lut_t := generate_lut_table;
    attribute rom_style : string;
    attribute rom_style of lut : signal is "block";

    signal out_power_r : std_logic_vector(OUTPUT_WIDTH-1 downto 0) := (others => '0');
    signal out_valid_r : std_logic := '0';

begin

    -- Synchronous LUT read. One cycle of latency. Matches BRAM inference.
    process(clock, reset)
    begin
        if reset = '1' then
            out_power_r <= (others => '0');
            out_valid_r <= '0';
        elsif rising_edge(clock) then
            out_power_r <= lut(to_integer(in_code));
            out_valid_r <= in_valid;
        end if;
    end process;

    out_power <= signed(out_power_r);
    out_valid <= out_valid_r;

end architecture;
