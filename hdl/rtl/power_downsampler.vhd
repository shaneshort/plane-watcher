-- =============================================================================
-- power_downsampler.vhd -- Fractional-rate scalar power resampler
-- =============================================================================
--
-- Converts the vendor ingress power stream to the decoder's expected sample
-- rate using a simple phase accumulator. The current hardware path delivers
-- 30.72 MSPS from the AD936x side, while the downstream decoder is written
-- around an exact 16.00 MSPS contract. A fixed "/4" downsampler only yields
-- 15.36 MSPS, which is close but wrong enough to break the preamble timing.
--
-- This block emits samples at OUTPUT_RATE_HZ by selecting the nearest input
-- sample whenever the fractional phase crosses the output boundary.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity power_downsampler is
    generic (
        INPUT_RATE_HZ  : positive := 30_720_000;
        OUTPUT_RATE_HZ : positive := SAMPLE_RATE_HZ
    );
    port (
        clock     : in  std_logic;
        reset     : in  std_logic;
        in_power  : in  signed(INPUT_POWER_WIDTH-1 downto 0);
        in_valid  : in  std_logic;
        out_power : out signed(INPUT_POWER_WIDTH-1 downto 0);
        out_valid : out std_logic
    );
end entity;

architecture rtl of power_downsampler is
    signal phase_accum : natural range 0 to INPUT_RATE_HZ - 1 := 0;
begin

    process(clock, reset)
        variable next_phase : natural;
    begin
        if reset = '1' then
            phase_accum <= 0;
            out_power <= (others => '0');
            out_valid <= '0';
        elsif rising_edge(clock) then
            out_valid <= '0';

            if in_valid = '1' then
                next_phase := phase_accum + OUTPUT_RATE_HZ;

                if next_phase >= INPUT_RATE_HZ then
                    phase_accum <= next_phase - INPUT_RATE_HZ;
                    out_power <= in_power;
                    out_valid <= '1';
                else
                    phase_accum <= next_phase;
                end if;
            end if;
        end if;
    end process;

end architecture;
