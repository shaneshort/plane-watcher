-- =============================================================================
-- message_aggregator.vhd — Round-Robin Message Collector
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
--
-- With NUM_DECODERS (typically 8) parallel message decoders, decoded messages
-- can arrive from any decoder at any time. This module collects them into a
-- single output stream using a holding register per decoder and a round-robin
-- arbiter.
--
-- Architecture:
--   1. Holding registers: Each decoder has a holding slot (holding_t record).
--      When a decoder asserts in_valid(i), its message is captured in
--      holding(i) — unless that slot is already full (in which case the
--      message is lost, with a simulation warning).
--
--   2. Round-robin arbiter: A rotating index (idx) cycles through all
--      decoder slots every clock. When it finds a full slot, the message
--      is sent to the output, the slot is cleared, and the packet counter
--      is decremented.
--
--   3. Packet timeout: The PACKET_TIMEOUT and MSGS_PER_TIMEOUT generics
--      implement a framing mechanism. After PACKET_TIMEOUT clock cycles
--      (default: 320,000 = ~20 ms at 16 MHz), if the message count hasn't
--      reached zero, empty padding messages (all zeros) are inserted to
--      fill out the packet. This ensures regular output even during quiet
--      periods, which is useful for the downstream Beast binary formatter.
--
-- Output format:
--   out_message is 128 bits wide: [111:0] = 112-bit Mode-S message,
--   [15:0] = 16-bit status/tag field (currently 0x0001 for valid messages,
--   0x0000 for timeout padding).
--
-- Timestamp (TOA) is carried alongside messages through the holding registers
-- and output on a separate port (out_toa) when a valid message is emitted.
-- =============================================================================

library ieee ;
    use ieee.std_logic_1164.all ;
    use ieee.numeric_std.all ;

library work ;
    use work.adsb_pkg.all ;

entity message_aggregator is
  generic (
    MSGS_PER_TIMEOUT    :       positive    := 128 ;             -- Messages per timeout packet
    PACKET_TIMEOUT      :       positive    := 32000000/100 ;    -- Timeout in clocks (~20 ms at 16 MHz... but 32 MHz?)
    NUM_DECODERS        :       positive    := 8                 -- Number of parallel decoder inputs
  ) ;
  port (
    clock               :   in  std_logic ;                                      -- System clock
    reset               :   in  std_logic ;                                      -- Asynchronous reset

    in_messages         :   in  messages_t(NUM_DECODERS-1 downto 0) ;            -- Decoded messages from all decoders
    in_toas             :   in  toas_t(NUM_DECODERS-1 downto 0) ;               -- Per-decoder Time-Of-Arrival
    in_rpls             :   in  rpls_t(NUM_DECODERS-1 downto 0) ;               -- Per-decoder Reference Power Level
    in_valid            :   in  std_logic_vector(NUM_DECODERS-1 downto 0) ;      -- Valid flags from all decoders

    out_message         :   out std_logic_vector(127 downto 0) ;                 -- Aggregated output (112-bit msg + 16-bit tag)
    out_toa             :   out unsigned(COUNTER_WIDTH-1 downto 0) ;             -- TOA for current output message
    out_rpl             :   out signed(INPUT_POWER_WIDTH-1 downto 0) ;           -- RPL for current output message
    out_valid           :   out std_logic ;                                      -- Output valid strobe
    debug_drop_count    :   out unsigned(31 downto 0)                           -- Messages dropped because holding slot was full
  ) ;
end entity ;

architecture arch of message_aggregator is

    -- Holding register record: one per decoder
    type holding_t is record
        msg     :   std_logic_vector(111 downto 0) ;    -- Buffered message
        toa     :   unsigned(COUNTER_WIDTH-1 downto 0) ;-- Latched TOA for this message
        rpl     :   signed(INPUT_POWER_WIDTH-1 downto 0);-- Signal level
        valid   :   std_logic ;                          -- Slot occupied
    end record ;

    type holdings_t is array(natural range <>) of holding_t ;

    -- One holding slot per decoder
    signal holding  :   holdings_t(NUM_DECODERS-1 downto 0) ;

    -- Per-decoder clear signals (asserted by round-robin when a slot is read)
    signal clear    :   std_logic_vector(NUM_DECODERS-1 downto 0) ;
    signal debug_drop_count_i : unsigned(31 downto 0) := (others => '0');

begin

    -- =========================================================================
    -- Holding register process
    --
    -- Captures decoded messages from each decoder into per-decoder holding
    -- slots. A slot can only be written when it's empty (valid = '0').
    -- If a decoder produces a new message while its slot is still full,
    -- the message is lost (this shouldn't happen in practice because the
    -- round-robin reads slots faster than decoders produce messages).
    -- =========================================================================
    hold_msg : process(clock, reset)
    begin
        if( reset = '1' ) then
            for i in holding'range loop
                holding(i).valid <= '0' ;
            end loop ;
            debug_drop_count_i <= (others => '0');
        elsif( rising_edge(clock) ) then
            for i in holding'range loop
                if( clear(i) = '1' ) then
                    -- Round-robin has consumed this slot — mark empty
                    holding(i).valid <= '0' ;
                else
                    if( in_valid(i) = '1' ) then
                        if( holding(i).valid = '0' ) then
                            -- Slot is empty — capture message, TOA, and RPL
                            holding(i).msg <= in_messages(i) ;
                            holding(i).toa <= in_toas(i) ;
                            holding(i).rpl <= in_rpls(i) ;
                            holding(i).valid <= '1' ;
                        else
                            -- Slot is full — message lost! (simulation warning only)
                            debug_drop_count_i <= debug_drop_count_i + 1;
                            report "Lost a message in aggregator" severity warning ;
                        end if ;
                    end if ;
                end if ;
            end loop ;
        end if ;
    end process ;

    -- =========================================================================
    -- Round-robin arbiter process
    --
    -- Cycles through decoder slots checking for full holding registers.
    -- When found, the message is output with a 0x0001 tag and the slot
    -- is cleared.
    --
    -- Packet framing:
    --   - 'count' starts at MSGS_PER_TIMEOUT-1 and decrements on each output
    --   - 'downcount' starts at PACKET_TIMEOUT and decrements each clock
    --   - If downcount expires with count > 0, zero-padded messages are
    --     emitted until count reaches 0, completing the packet
    --   - Both counters then reset for the next packet
    --
    -- The index rotates downward (N-1 → 0 → N-1) to distribute access
    -- fairly across all decoders.
    -- =========================================================================
    round_robin : process(clock, reset)
        variable count : natural range 0 to MSGS_PER_TIMEOUT-1 := MSGS_PER_TIMEOUT-1 ;
        variable downcount : natural range 0 to PACKET_TIMEOUT := PACKET_TIMEOUT ;
        variable idx : natural range 0 to NUM_DECODERS-1 := 0 ;
        variable new_count : natural range 0 to MSGS_PER_TIMEOUT-1 ;
    begin
        if( reset = '1' ) then
            idx := 0 ;
            count := MSGS_PER_TIMEOUT-1 ;
            downcount := PACKET_TIMEOUT ;
            out_valid <= '0' ;
            clear <= (others =>'0') ;
        elsif( rising_edge(clock) ) then
            -- Defaults: no output, no clears
            out_valid <= '0' ;
            clear <= (others =>'0') ;
            new_count := count ;

            -- Packet timeout logic: when the timer expires, emit zero-padding
            -- messages until the packet is full
            if( downcount > 0 ) then
                downcount := downcount - 1 ;
            else
                if( count > 0 ) then
                    -- Emit a zero-padded message to fill the packet
                    new_count := count - 1 ;
                    out_message <= (others =>'0') ;
                    out_toa <= (others => '0') ;
                    out_rpl <= (others => '0') ;
                    out_valid <= '1' ;
                else
                    -- Packet complete — reset for next packet
                    new_count := MSGS_PER_TIMEOUT-1 ;
                    downcount := PACKET_TIMEOUT ;
                end if ;
            end if ;

            -- Check the current decoder's holding slot
            if( holding(idx).valid = '1' ) then
                -- Output the message with 0x0001 status tag appended
                out_message <= holding(idx).msg & x"0001" ;
                out_toa <= holding(idx).toa ;
                out_rpl <= holding(idx).rpl ;
                out_valid <= '1' ;
                -- Decrement packet counter
                if( count > 0 ) then
                    new_count := count - 1 ;
                else
                    -- Packet boundary reached — reset counters
                    new_count := MSGS_PER_TIMEOUT-1 ;
                    if( downcount > 0 ) then
                        downcount := PACKET_TIMEOUT ;
                    end if ;
                end if ;
                -- Clear the holding slot so the decoder can reuse it
                clear(idx) <= '1' ;
            end if ;

            count := new_count ;

            -- Rotate to the next decoder slot (circular: N-1 → 0 → N-1)
            if( idx > 0 ) then
                idx := idx - 1 ;
            else
                idx := NUM_DECODERS - 1 ;
            end if ;
        end if ;
    end process ;

    debug_drop_count <= debug_drop_count_i;

end architecture ;
