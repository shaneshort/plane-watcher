-- =============================================================================
-- smallest_bsds.vhd — Weakest-Bit Tracker for Error Correction
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
--
-- This module tracks the N weakest bits (smallest |BSD| values) in a Mode-S
-- message as it's being demodulated. These are the bits most likely to have
-- been corrupted by noise, and are candidates for bit-flipping error correction
-- in the downstream bit_flipper module.
--
-- It maintains two independent rankings:
--   - ranks_ext:   Top 5 weakest bits across all 112 bit positions (extended messages)
--   - ranks_short: Top 5 weakest bits across the first 54 positions (short messages)
--
-- Short messages are only 56 bits total, but the last 24 bits are CRC (which
-- we don't flip), so only the first 32 payload bits + some overhead = ~54
-- useful bit positions are tracked for short messages.
--
-- Algorithm:
--   As each BSD arrives (one per demodulated bit), compare its absolute value
--   against the current rankings (sorted by ascending |BSD|, weakest first).
--   If the new BSD is smaller than any ranked entry (or a slot is unset),
--   insert it at that position and shift the remaining entries down. This is
--   an insertion sort that runs in O(N) per input bit, where N = 5.
--
-- The module also accumulates the original hard decisions (bhd) into the
-- 'bits' register, building up the full 112-bit decoded message as BSDs
-- arrive. This avoids needing a separate bit accumulator.
--
-- When all 112 bits have been received, the 'finished' output pulses.
--
-- The 'clear' input resets the rankings for a new message.
--
-- =============================================================================
-- Local package: smallest_bsds_p
-- =============================================================================
-- Defines the element record type used by both this module and bit_flipper.
-- Placed in its own package (not in adsb_pkg) because it's specific to the
-- error correction subsystem.
-- =============================================================================

library ieee ;
    use ieee.std_logic_1164.all ;
    use ieee.numeric_std.all ;

package smallest_bsds_p is

    -- A single ranked element: the BSD value, its bit index in the message,
    -- and whether this slot has been assigned a real value.
    type element_t is record
        value   :   signed(7 downto 0) ;       -- BSD value (signed, magnitude = confidence)
        index   :   integer range 0 to 111 ;   -- Bit position in the 112-bit message
        set     :   boolean ;                   -- True if this slot contains a valid entry
    end record ;

    -- Array of ranked elements (typically 5 deep = NUM_WEAK_BITS)
    type elements_t is array(natural range <>) of element_t ;

    -- Default "empty" element — used during reset and clear operations.
    -- The '-' (don't-care) value for 'value' allows the synthesiser to
    -- optimise the reset logic.
    constant UNSET : element_t := (
        value   => (others => '-'),
        index   => 0,
        set     => false
    ) ;

end package ;

-- =============================================================================
-- Entity: smallest_bsds
-- =============================================================================

library ieee ;
    use ieee.std_logic_1164.all ;
    use ieee.numeric_std.all ;

library work ;
    use work.smallest_bsds_p.all ;

entity smallest_bsds is
  port (
    clock           :   in  std_logic ;                        -- System clock
    reset           :   in  std_logic ;                        -- Asynchronous reset

    clear           :   in  std_logic ;                        -- Synchronous clear (start new message)

    finished        :   out std_logic ;                        -- Pulses when all 112 bits received

    bsd             :   in  signed(7 downto 0) ;               -- Bit Soft-Decision input
    bhd             :   in  std_logic ;                        -- Bit Hard Decision input (the actual decoded bit)
    bsd_valid       :   in  std_logic ;                        -- BSD input valid strobe

    bits            :   out std_logic_vector(111 downto 0) ;   -- Accumulated decoded message bits
    smallest_ext    :   out elements_t(0 to 4) ;               -- 5 weakest bits (extended message ranking)
    smallest_short  :   out elements_t(0 to 4)                 -- 5 weakest bits (short message ranking)
  ) ;
end entity ;

architecture arch of smallest_bsds is

    -- Internal copies of the rankings (written by process, read by outputs)
    signal ranks_ext    : elements_t(smallest_ext'range) ;
    signal ranks_short  : elements_t(smallest_short'range) ;

    -- Current bit index (0 to 111), incremented on each bsd_valid
    signal idx : integer range 0 to 111 ;

    -- Set after all 112 bits received; prevents further updates to bits/rankings
    -- from stale bsd_valid pulses (the upstream sample gate has a safety margin
    -- that continues forwarding samples beyond the actual message length).
    signal done : std_logic ;

begin

    -- =========================================================================
    -- Ranking process: insertion sort of incoming BSDs
    --
    -- On each bsd_valid pulse:
    --   1. Shift the new hard decision bit into the 'bits' accumulator
    --   2. Compare abs(bsd) against each ranked slot:
    --      - If the slot is unset, or the new BSD is weaker (smaller |value|),
    --        insert the new entry here and shift everything below it down by one.
    --      - Exit the loop after the first insertion (highest priority = weakest).
    --   3. Repeat for both extended and short rankings.
    --   4. Increment the bit index.
    --
    -- The short ranking only tracks the first 32 payload bits.
    -- Short messages (DF 0/4/5/11) are 56 bits: 32 payload + 24 PI/CRC.
    -- Only payload bits are candidates for error correction — flipping
    -- PI/CRC bits is semantically wrong (DF11: changes the CRC target;
    -- DF0/4/5: changes the inferred interrogator identity).
    --
    -- Early completion: when 56 bits have been accumulated and the DF
    -- field indicates a short message (DF MSB = '0'), finished is
    -- asserted immediately instead of waiting for the full 112 bits.
    -- This halves decoder occupancy for short-frame traffic.
    -- =========================================================================
    compare : process(clock, reset)
        variable next_bits : std_logic_vector(111 downto 0);
    begin
        if( reset = '1' ) then
            -- Clear all ranking slots on reset
            for i in ranks_ext'range loop
                ranks_ext(i) <= UNSET ;
                ranks_short(i) <= UNSET ;
            end loop ;
            bits <= (others => '0') ;
            idx <= 0 ;
            finished <= '0' ;
            done <= '0' ;
        elsif( rising_edge(clock) ) then
            finished <= '0' ;
            if( clear = '1' ) then
                -- Synchronous clear: reset rankings for a new message
                for i in ranks_ext'range loop
                    ranks_ext(i) <= UNSET ;
                    ranks_short(i) <= UNSET ;
                end loop ;
                bits <= (others => '0') ;
                idx <= 0 ;
                done <= '0' ;
            else
                if( bsd_valid = '1' and done = '0' ) then
                    -- synthesis translate_off
                    if idx < 24 then
                        report "SMALL_DBG: idx=" & integer'image(idx) &
                               " bhd=" & std_logic'image(bhd) &
                               " bsd=" & integer'image(to_integer(bsd));
                    end if;
                    -- synthesis translate_on

                    -- Accumulate the hard decision bit (shift left, new bit enters LSB)
                    next_bits := bits(bits'high-1 downto 0) & bhd;
                    bits <= next_bits ;

                    -- === Extended message ranking (all 112 bits) ===
                    for i in ranks_ext'range loop
                        -- Insert if: slot is empty OR new BSD is weaker than current
                        if( ranks_ext(i).set = false or abs(bsd) < abs(ranks_ext(i).value) ) then
                            -- Place the new entry at position i
                            ranks_ext(i).value <= bsd ;
                            ranks_ext(i).index <= idx ;
                            ranks_ext(i).set <= true ;
                            -- Shift all entries below position i down by one
                            -- (the weakest entry at the bottom falls off)
                            for x in i+1 to ranks_ext'high loop
                                ranks_ext(x) <= ranks_ext(x-1) ;
                            end loop ;
                            exit ;  -- Only insert once per BSD
                        end if ;
                    end loop ;

                    -- === Short message ranking (first 32 payload bits) ===
                    if(idx < 32 ) then
                        for i in ranks_short'range loop
                            if( ranks_short(i).set = false or abs(bsd) < abs(ranks_short(i).value ) ) then
                                ranks_short(i).value <= bsd ;
                                ranks_short(i).index <= idx ;
                                ranks_short(i).set <= true ;
                                for x in i+1 to ranks_short'high loop
                                    ranks_short(x) <= ranks_short(x-1) ;
                                end loop ;
                                exit ;
                            end if ;
                        end loop ;
                    end if ;

                    -- Advance the bit index (0 → 111). For a short frame, after
                    -- 56 accumulated bits the message still lives in the lower
                    -- half of next_bits, so realign it into bits(111:56) before
                    -- signalling completion. This preserves the same index map
                    -- used by valid_df(), calculate_flip_mask(), and swizzle().
                    if idx = 55 and next_bits(55) = '0' then
                        bits <= next_bits(55 downto 0) & x"00000000000000";
                        finished <= '1';
                        done <= '1';
                    elsif( idx < 111 ) then
                        idx <= idx + 1 ;
                    else
                        -- All 112 bits received — signal completion
                        finished <= '1' ;
                        done <= '1' ;
                    end if ;
                end if ;
            end if ;
        end if ;
    end process ;

    -- Output the internal rankings
    smallest_ext <= ranks_ext ;
    smallest_short <= ranks_short ;

end architecture ;
