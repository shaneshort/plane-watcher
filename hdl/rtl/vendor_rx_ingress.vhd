-- =============================================================================
-- vendor_rx_ingress.vhd -- Vendor-radio ingress adapter for current decoder
-- =============================================================================
--
-- Adapts the vendor board's natural 16-bit I/Q receive stream at 30.72 MHz
-- into the existing decoder's scalar power stream. This keeps the decoder
-- core unchanged for first hardware bring-up.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity vendor_rx_ingress is
    generic (
        IQ_WIDTH        : positive := 16;
        INPUT_RATE_HZ   : positive := 30_720_000;
        OUTPUT_RATE_HZ  : positive := SAMPLE_RATE_HZ
    );
    port (
        clock        : in  std_logic;
        reset        : in  std_logic;
        rx_i         : in  signed(IQ_WIDTH-1 downto 0);
        rx_q         : in  signed(IQ_WIDTH-1 downto 0);
        rx_valid     : in  std_logic;
        raw_power_dbg : out signed(INPUT_POWER_WIDTH-1 downto 0);
        raw_valid_dbg : out std_logic;
        sample_power : out signed(INPUT_POWER_WIDTH-1 downto 0);
        sample_valid : out std_logic
    );
end entity;

architecture rtl of vendor_rx_ingress is
    signal power_raw   : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal power_valid : std_logic;
    signal raw_power_dbg_r : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal raw_valid_dbg_r : std_logic := '0';
begin

    raw_power_dbg <= raw_power_dbg_r;
    raw_valid_dbg <= raw_valid_dbg_r;

    U_iq_to_power : entity work.iq_to_power
        generic map (
            IQ_WIDTH => IQ_WIDTH
        )
        port map (
            clock     => clock,
            reset     => reset,
            in_i      => rx_i,
            in_q      => rx_q,
            in_valid  => rx_valid,
            out_power => power_raw,
            out_valid => power_valid
        );

    U_downsampler : entity work.power_downsampler
        generic map (
            INPUT_RATE_HZ  => INPUT_RATE_HZ,
            OUTPUT_RATE_HZ => OUTPUT_RATE_HZ
        )
        port map (
            clock     => clock,
            reset     => reset,
            in_power  => power_raw,
            in_valid  => power_valid,
            out_power => sample_power,
            out_valid => sample_valid
        );

    -- Keep debug-only observation off the immediate iq_to_power output fanout.
    -- The counters in adsb_vendor_wrapper only need approximate statistics, so
    -- a one-cycle-delayed copy is fine and gives the raw-power datapath its own
    -- register boundary before the compare/increment logic.
    debug_tap : process(clock, reset)
    begin
        if reset = '1' then
            raw_power_dbg_r <= (others => '0');
            raw_valid_dbg_r <= '0';
        elsif rising_edge(clock) then
            raw_power_dbg_r <= power_raw;
            raw_valid_dbg_r <= power_valid;
        end if;
    end process;

end architecture;
