-- =============================================================================
-- adsb_crc.vhd — CRC-24 Validator for Mode-S Messages
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
--
-- Mode-S uses a 24-bit CRC (Cyclic Redundancy Check) to detect transmission
-- errors. The generator polynomial is:
--
--   x^24 + x^23 + x^22 + x^21 + x^20 + x^19 + x^18 + x^17 +
--   x^16 + x^15 + x^14 + x^13 + x^12 + x^10 + x^3 + 1
--   = 0x1FFF409 (25-bit representation with leading 1)
--   (Kasami-Matoba 1964, optimised for burst error detection)
--
-- In a valid Mode-S message, the CRC is appended to the payload such that
-- dividing the entire message (payload + CRC) by the polynomial yields a
-- zero remainder. We exploit this: if the remainder is zero and the message
-- is non-trivial, the CRC passes.
--
-- The module handles both message formats:
--   - Short messages (DF 0-15): 56 bits = 7 bytes (32-bit payload + 24-bit CRC)
--   - Extended messages (DF 16-31): 112 bits = 14 bytes (88-bit payload + 24-bit CRC)
--
-- The format is determined by bit 7 of the first byte (MSB of the Downlink
-- Format field). If DF >= 16, bit 7 is '1' → extended. Otherwise → short.
--
-- Processing is byte-at-a-time (8 bits per clock cycle), so a short message
-- takes 7 clocks and an extended message takes 14 clocks to validate.
--
-- Architecture: Two-process FSM (Mealy-style)
--   sync: Clocked process that registers the next state on each rising edge.
--   comb: Combinatorial process that computes the next state from current state.
--
-- This pattern separates sequential logic (sync) from combinatorial logic
-- (comb), making the FSM easier to reason about and synthesise predictably.
--
-- FSM states:
--   IDLE        — Waiting for data_valid. Accepts new message.
--   CALCULATING — Processing one byte per clock. Shifts data right by 8,
--                 feeds 8 bits through the CRC polynomial. Counts down
--                 from (num_bytes - 1) to 0.
--   DONE        — Checks final CRC remainder. If zero AND message was
--                 non-zero (nonzero flag set), asserts crc_good. Transitions
--                 back to IDLE on the next clock.
--
-- The "nonzero" check prevents an all-zero message from passing CRC (since
-- 0 mod anything = 0). This rejects noise bursts that happen to be all zeros.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity adsb_crc is
    port (
        clock      : in  std_logic;                       -- System clock
        reset      : in  std_logic;                       -- Asynchronous reset
        busy       : out std_logic;                       -- High while CRC computation is in progress
        data       : in  std_logic_vector(111 downto 0);  -- Full 112-bit message (short messages zero-padded in upper bits)
        data_valid : in  std_logic;                       -- Pulse high for one clock to start CRC computation
        crc        : out std_logic_vector(23 downto 0);   -- Final CRC-24 remainder (zero if message is valid)
        crc_good   : out std_logic;                       -- High for one clock if CRC passed (remainder = 0, message non-zero)
        crc_valid  : out std_logic                        -- High for one clock when CRC result is ready
    );
end entity;

architecture rtl of adsb_crc is

    -- CRC-24 polynomial with leading '1' bit (25 bits total).
    -- Binary: 1_1111_1111_1111_0100_0000_1001
    constant CRC_POLY : std_logic_vector(24 downto 0) := 25x"1fff409";

    -- FSM state type
    type fsm_t is (IDLE, CALCULATING, DONE);

    -- All FSM state bundled into a single record for clean two-process style.
    -- This makes it easy to see all state in one place and ensures nothing
    -- is accidentally left unassigned (the "future <= current" default at
    -- the top of the comb process handles that).
    type state_t is record
        fsm     : fsm_t;                                  -- Current FSM state
        count   : natural range 0 to 14-1;                -- Byte countdown (13 for extended, 6 for short)
        data    : std_logic_vector(111 downto 0);         -- Working copy of message, shifted right 8 bits per cycle
        crc     : std_logic_vector(24 downto 0);          -- 25-bit CRC accumulator (bit 24 is the overflow bit)
        busy    : std_logic;                              -- Registered busy output
        valid   : std_logic;                              -- Registered valid output (pulses on completion)
        good    : std_logic;                              -- Registered good output (CRC passed)
        nonzero : std_logic;                              -- Set if any non-zero byte has been seen
    end record;

    signal current, future : state_t;

begin

    -- =========================================================================
    -- Synchronous process: register the next state on each clock edge.
    -- Only the FSM state and busy are explicitly reset; everything else
    -- will be initialised when a new message arrives (IDLE → CALCULATING).
    -- =========================================================================
    sync : process(clock, reset)
    begin
        if reset = '1' then
            current.fsm  <= IDLE;
            current.busy <= '1';
        elsif rising_edge(clock) then
            current <= future;
        end if;
    end process;

    -- =========================================================================
    -- Combinatorial process: compute the next state from the current state.
    --
    -- The CRC calculation unrolls 8 iterations of the standard bit-serial
    -- CRC algorithm per clock cycle (one byte at a time). For each bit:
    --   1. Shift the CRC register left by 1, shifting in the next data bit.
    --   2. If the MSB (bit 24) that shifted out is '1', XOR with the polynomial.
    --
    -- Data is consumed LSByte-first: the lowest 8 bits of the working register
    -- are processed each cycle, then the register shifts right by 8 to expose
    -- the next byte. This matches the wire order of Mode-S messages.
    -- =========================================================================
    comb : process(all)
        variable crc_v : std_logic_vector(current.crc'range);
    begin
        -- Default: hold current state (prevents latches)
        future <= current;

        case current.fsm is
            when IDLE =>
                future.busy    <= '0';
                future.valid   <= '0';
                future.good    <= '0';
                future.nonzero <= '0';

                if data_valid = '1' then
                    future.data <= data;
                    future.fsm  <= CALCULATING;
                    future.crc  <= (others => '0');
                    future.busy <= '1';

                    -- Determine message length from DF field.
                    -- Bit 7 of the first byte is the MSB of the 5-bit DF field.
                    -- DF >= 16 means bit 7 = '1' → extended (14 bytes).
                    -- DF < 16 means bit 7 = '0' → short (7 bytes).
                    if data(7) = '1' then
                        future.count <= 14-1;   -- Extended: 14 bytes to process
                    else
                        future.count <= 7-1;    -- Short: 7 bytes to process
                    end if;
                end if;

            when CALCULATING =>
                -- Track whether we've seen any non-zero byte.
                -- This prevents all-zero messages from "passing" CRC
                -- (since CRC of all-zeros is zero, which would be a false positive).
                if current.nonzero = '0' and
                   unsigned(current.data(7 downto 0)) /= 0 then
                    future.nonzero <= '1';
                end if;

                -- Shift data right by 8 bits (zero-fill from the top),
                -- consuming the lowest byte for CRC this cycle.
                future.data <= x"00" & current.data(current.data'high downto 8);

                -- Unrolled CRC: process 8 bits (one byte) per clock cycle.
                -- Bits are processed from bit 7 down to bit 0 of the current
                -- lowest byte, matching the MSB-first transmission order.
                crc_v := current.crc;
                for i in 7 downto 0 loop
                    -- Shift left by 1, bringing in the next data bit
                    crc_v := crc_v(crc_v'high-1 downto 0) & current.data(i);
                    -- If the bit that shifted out (now in bit 24) is '1',
                    -- XOR with the generator polynomial
                    if crc_v(crc_v'high) = '1' then
                        crc_v := crc_v xor CRC_POLY;
                    end if;
                end loop;
                future.crc <= crc_v;

                -- Count down bytes remaining
                if current.count = 0 then
                    future.fsm <= DONE;        -- All bytes processed
                else
                    future.count <= current.count - 1;
                end if;

            when DONE =>
                -- Evaluate the final CRC remainder.
                -- A valid message produces a zero remainder (the CRC appended
                -- to the payload cancels out perfectly). We also require at
                -- least one non-zero byte to reject trivial all-zero inputs.
                future.fsm   <= IDLE;
                future.valid <= '1';
                if to_integer(unsigned(current.crc)) = 0 and
                   current.nonzero = '1' then
                    future.good <= '1';
                else
                    future.good <= '0';
                end if;
                future.busy <= '0';

            when others =>
                -- Catch-all for synthesis safety
                future.fsm <= IDLE;
        end case;
    end process;

    -- Output assignments from registered state
    busy      <= current.busy;
    crc_good  <= current.good;
    crc_valid <= current.valid;
    crc       <= current.crc(crc'range);  -- Lower 24 bits of the 25-bit accumulator

end architecture;
