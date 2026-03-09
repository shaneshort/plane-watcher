-- =============================================================================
-- timestamp_counter.vhd — Hardware Timing Core for MLAT
-- =============================================================================
--
-- This module is the timing backbone of the ADS-B receiver. It maintains a
-- free-running 64-bit counter clocked by the local decode clock, and captures the
-- counter value on GNSS PPS (Pulse Per Second) rising edges.
--
-- This is a NEW module (not from bladeRF-adsb). The bladeRF design has no
-- timestamping — this is our key addition for MLAT support.
--
-- MLAT (Multilateration) determines aircraft position by comparing the
-- Time-Of-Arrival (TOA) of the same transmission at multiple receivers.
-- For this to work, each receiver must timestamp packets with sub-microsecond
-- precision relative to a common time reference (UTC via GNSS).
--
-- How it works:
--   1. The counter increments every input clock cycle
--   2. When a PPS pulse arrives from the GNSS module, the current counter
--      value is latched into counter_at_pps
--   3. When the preamble detector finds an ADS-B packet, it latches the
--      counter value as the Time-Of-Arrival
--   4. Linux reads both values and computes UTC time:
--
--      absolute_time = UTC_second + (toa_counter - counter_at_pps) / counter_rate
--
-- At 100 MHz, the raw timestamp resolution is 10 ns. With quadratic peak
-- interpolation from the correlator, effective precision is still set by the
-- detector math rather than the counter granularity.
--
-- The 64-bit counter at 100 MHz rolls over after 2^64 / 100e6 ≈ 5,849 years.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity timestamp_counter is
    generic (
        WIDTH : positive := COUNTER_WIDTH  -- Counter width in bits (default 64)
    );
    port (
        clock          : in  std_logic;                         -- Counter clock
        reset          : in  std_logic;                         -- Synchronous reset

        -- Current counter value — read by preamble detector to stamp packets
        counter_value  : out unsigned(WIDTH-1 downto 0);

        -- PPS interface — directly from GNSS module GPIO pin
        pps_in         : in  std_logic;                         -- Raw PPS input (asynchronous!)
        counter_at_pps : out unsigned(WIDTH-1 downto 0);        -- Counter value latched at last PPS edge
        pps_count      : out unsigned(31 downto 0);             -- Total PPS events seen (diagnostic)
        pps_new        : out std_logic                          -- Single-cycle pulse on each PPS event
    );
end entity;

architecture rtl of timestamp_counter is

    -- Free-running counter — increments every clock cycle
    signal count       : unsigned(WIDTH-1 downto 0) := (others => '0');

    -- PPS synchroniser shift register (3 bits):
    --   pps_sr(0) = raw input (may be metastable!)
    --   pps_sr(1) = first flop output (resolved, but may still glitch)
    --   pps_sr(2) = second flop output (safe to use for logic)
    --
    -- This is a classic double-flop synchroniser. The PPS signal is
    -- asynchronous to our local counter clock — it comes from the GNSS module
    -- on its own timing. Sampling an async signal directly risks
    -- metastability (the flop output settles to an unpredictable value).
    -- Two back-to-back flip-flops reduce the probability of metastable
    -- output to negligible levels (MTBF of millions of years).
    signal pps_sr      : std_logic_vector(2 downto 0) := (others => '0');
    attribute ASYNC_REG : string;
    attribute ASYNC_REG of pps_sr : signal is "TRUE";

    -- Rising edge detect: high for exactly one clock when PPS transitions 0→1.
    -- pps_sr(1) is the current stable value, pps_sr(2) is the previous cycle.
    -- When pps_sr(1)='1' AND pps_sr(2)='0', we've just seen a rising edge.
    signal pps_rising  : std_logic;

    -- PPS event counter — increments on each PPS, useful for diagnostics
    -- (Linux can check this to verify PPS is alive and counting)
    signal pps_cnt     : unsigned(31 downto 0) := (others => '0');

    -- Latched counter value at last PPS — this is the anchor point that
    -- lets Linux convert raw counter values to UTC time
    signal pps_latch   : unsigned(WIDTH-1 downto 0) := (others => '0');

begin

    -- Rising edge detection: combinatorial decode of the synchroniser output.
    -- Goes high for exactly one clock cycle on each PPS rising edge.
    pps_rising <= pps_sr(1) and not pps_sr(2);

    process(clock)
    begin
        if rising_edge(clock) then
            if reset = '1' then
                count      <= (others => '0');
                pps_sr     <= (others => '0');
                pps_cnt    <= (others => '0');
                pps_latch  <= (others => '0');
                pps_new    <= '0';
            else
                -- Free-running counter — never stops, never resets (except on reset)
                count <= count + 1;

                -- Shift the PPS input through the synchroniser chain:
                -- Each clock cycle, the raw pps_in enters bit 0, and the
                -- previous values shift up. After 2 clock cycles, the
                -- signal in pps_sr(1) and pps_sr(2) is safe for logic.
                pps_sr <= pps_sr(1 downto 0) & pps_in;

                -- On PPS rising edge: latch the counter and notify
                if pps_rising = '1' then
                    pps_latch <= count;      -- Snapshot counter for UTC alignment
                    pps_cnt   <= pps_cnt + 1; -- Diagnostic counter
                    pps_new   <= '1';         -- Single-cycle notification pulse
                else
                    pps_new   <= '0';
                end if;
            end if;
        end if;
    end process;

    -- Output assignments
    counter_value  <= count;       -- Live counter (read by preamble detector)
    counter_at_pps <= pps_latch;   -- Last PPS snapshot (read by Linux)
    pps_count      <= pps_cnt;     -- Diagnostic (read by Linux)

end architecture;
