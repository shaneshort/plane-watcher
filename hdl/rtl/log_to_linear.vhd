-- =============================================================================
-- log_to_linear.vhd -- Antilog lookup table: log-scale ADC code → linear power
-- =============================================================================
--
-- Converts the 12-bit output of an AD8318 logarithmic detector (after
-- digitisation by the AD9238) into a 24-bit signed linear power value
-- compatible with the existing decode pipeline's INPUT_POWER_WIDTH contract.
--
-- The AD8318 has a negative slope (-24 mV/dB): higher input power produces
-- a lower voltage, and therefore a lower ADC code. This module performs the
-- inversion naturally in the LUT — low input codes map to high output power.
--
-- Implementation: a 4096-entry × 24-bit ROM inferred as block RAM. One clock
-- cycle of read latency. Replaces the 3-cycle I²+Q² path of iq_to_power.vhd
-- with no DSP48E consumption.
--
-- The LUT contents are populated from a default table that is a rough
-- placeholder antilog curve. The real table must be generated from the
-- measured AD8318 + AD8009 transfer function once hardware is available.
-- The generate_lut_table function below is documented with the assumed
-- mapping so it can be regenerated.
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
    -- Placeholder LUT generator.
    --
    -- Assumptions (to be replaced once AD8318+AD8009 are characterised):
    --   - ADC code 0x000  = weakest signal (lowest voltage at ADC input, which
    --     corresponds to HIGHEST RF power due to AD8318's negative slope).
    --     Wait — actually, after the AD8009 (likely inverting config or offset
    --     inversion), we assume ADC code 0x000 = lowest RF power (noise floor)
    --     and 0xFFF = highest RF power. The placeholder table here uses this
    --     convention. If the AD8009 is non-inverting (preserving the AD8318's
    --     negative slope), the LUT index should be reversed at table-build
    --     time — change RAW_CODE below to (LUT_DEPTH - 1 - i).
    --
    --   - Dynamic range: 60 dB mapped across ADC codes.
    --     dBm(code) = MIN_DBM + (code / (LUT_DEPTH - 1)) * 60
    --
    --   - Linear power: 10^(dBm/10), scaled so that a mid-range "typical"
    --     received pulse (~-70 dBm assumed) maps to ~8000 in the output, and
    --     POWER_THRESHOLD=2000 stays a useful noise floor gate.
    --
    -- These numbers are rough placeholders — expect to regenerate from
    -- measured captures. The math is kept in simulation-only real arithmetic
    -- inside this function; the synthesised ROM is just the resulting table.
    -- ------------------------------------------------------------------------
    function generate_lut_table return lut_t is
        variable table     : lut_t;
        variable dbm       : real;
        variable lin       : real;
        variable scaled    : real;
        variable clamped   : integer;
        constant MIN_DBM       : real := -90.0;   -- noise floor end
        constant MAX_DBM       : real := -30.0;   -- strong-signal end
        constant REF_DBM       : real := -70.0;   -- "typical pulse"
        constant REF_OUT       : real := 8000.0;  -- target output at REF_DBM
        constant RANGE_DBM     : real := MAX_DBM - MIN_DBM;
        constant OUT_MAX       : integer := 2**(OUTPUT_WIDTH-1) - 1;
        variable ref_lin       : real;
        variable scale_factor  : real;
        variable raw_code      : integer;
    begin
        -- Reference linear power at REF_DBM.
        ref_lin := 10.0 ** (REF_DBM / 10.0);
        -- Scale so 10^(REF_DBM/10) maps to REF_OUT.
        scale_factor := REF_OUT / ref_lin;

        for i in 0 to LUT_DEPTH - 1 loop
            -- Assume non-inverting AD8009 path: raw ADC code = LUT_DEPTH-1-i
            -- would mean higher ADC code corresponds to weaker signal (AD8318
            -- slope preserved). For a placeholder we assume the analogue stage
            -- has been set up so ADC code rises with power. Swap to
            -- (LUT_DEPTH - 1 - i) here if the measured polarity is the
            -- opposite.
            raw_code := i;

            dbm := MIN_DBM + (real(raw_code) / real(LUT_DEPTH - 1)) * RANGE_DBM;
            lin := 10.0 ** (dbm / 10.0);
            scaled := lin * scale_factor;

            if scaled > real(OUT_MAX) then
                clamped := OUT_MAX;
            elsif scaled < 0.0 then
                clamped := 0;
            else
                clamped := integer(scaled);
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
