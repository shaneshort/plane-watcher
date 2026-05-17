-- =============================================================================
-- log_to_linear.vhd -- Antilog lookup table: log-scale ADC code → linear power
-- =============================================================================
--
-- Converts the output of the ADL5513 logarithmic detector frontend digitised
-- by the AD9203 prototype ADC into a 24-bit signed linear power value
-- compatible with the existing decode pipeline's INPUT_POWER_WIDTH contract.
--
-- The ADL5513 measurement-mode output increases linear-in-dB with RF input
-- amplitude. The prototype ties the AD9203 for straight-binary output, so
-- higher detector voltage gives higher FPGA ADC code.
--
-- Implementation: a 2**ADC_WIDTH by OUTPUT_WIDTH ROM inferred as block RAM.
-- One clock cycle of read latency. No DSP48E consumption.
--
-- The LUT is generated at elaboration from `generate_lut_table`. The current
-- table is a first-light placeholder based on the ADL5513's positive
-- 21 mV/dB nominal transfer; replace the ADC-code anchors with measured
-- prototype board captures before treating RPL as calibrated.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;
    use ieee.math_real.all;

library work;
    use work.adsb_pkg.all;

entity log_to_linear is
    generic (
        ADC_WIDTH    : positive := 10;
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
    -- LUT generator — ADL5513 prototype transfer function -> linear power.
    --
    -- Calibrate directly in ADC-code space. The ADL5513, AD8138, AD9203 input
    -- span, and FPGA pin capture form the signal the decoder actually
    -- consumes, so board-level captures should be the source of truth.
    --
    -- Placeholder anchors:
    --   * ADL5513 datasheet: VOUT rises with input level at nominal 21 mV/dB.
    --   * AD9203 datasheet: 10-bit straight-binary CMOS output.
    --   * Until prototype captures exist, spread an 80 dB detector window
    --     across the useful 10-bit ADC range and clamp at both ends. This
    --     keeps preamble detection usable for bring-up but is not calibrated.
    --
    -- Output scaling:
    --   * Target: a pulse at REF_DBM maps to REF_OUT counts.
    --   * Out-of-range codes clamp to 0 (below noise floor) or OUT_MAX
    --     (saturation). Saturation still produces max output so preamble
    --     detection triggers on very strong bursts.
    --
    -- The prototype placeholder assumes positive polarity: higher RF power
    -- gives a higher ADC code. If measured board data proves otherwise, replace
    -- this table with measured anchors rather than inverting elsewhere.
    -- ------------------------------------------------------------------------
    function generate_lut_table return lut_t is
        variable table     : lut_t;
        variable dbm       : real;
        variable lin       : real;
        variable scaled    : real;
        variable clamped   : integer;

        -- Detector calibration anchors in 10-bit FPGA ADC code space.
        -- Replace these with measured ADL5513 -> AD8138 -> AD9203 captures.
        constant CAL_POINT_COUNT : positive := 6;
        type int_array_t is array (0 to CAL_POINT_COUNT - 1) of integer;
        type real_array_t is array (0 to CAL_POINT_COUNT - 1) of real;
        constant CODE_FLOOR : integer := 64;
        constant CODE_POINTS : int_array_t := (
            192, 320, 448, 576, 704, 832
        );
        constant DBM_POINTS : real_array_t := (
            -70.0, -60.0, -50.0, -40.0, -30.0, -20.0
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
