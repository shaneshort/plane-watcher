-- =============================================================================
-- bit_flipper.vhd — Brute-Force Error Correction for Mode-S Messages
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
--
-- This module implements soft-decision error correction by brute-force
-- searching over the weakest bits identified by the smallest_bsds module.
--
-- Strategy:
--   For DF11/DF17/DF18, given the 5 least-confident bits (smallest |BSD|
--   values), try all 2^5 = 32 combinations of flipping those bits. For each
--   combination, run CRC-24. If CRC passes, we've recovered the correct
--   message. If no combination passes after all 32 tries, the message is
--   unrecoverable.
--
--   Other valid Mode-S DFs are forwarded as the raw iter=0 candidate and left
--   for host-side software to validate or filter. That keeps FPGA CRC/FEC
--   focused on DF11/DF17/DF18, which are the classes we want pre-checked in
--   hardware.
--
--   This "soft-decision brute-force" approach is the key technique that gives
--   commercial-grade ADS-B receivers their high decode rates. Most corrupted
--   messages have only 1–2 bit errors, which are found within the first few
--   iterations. The worst case (5 flips) still only takes 32 × ~15 clock
--   cycles ≈ 480 clocks ≈ 30 µs at 16 MHz, well within the inter-message gap.
--
-- Architecture:
--   The module contains two sub-components:
--     - smallest_bsds: Tracks the 5 weakest bits during BSD streaming
--     - adsb_crc: Validates each candidate message
--
--   FSM states:
--     IDLE               — Waiting for 'start' pulse
--     WAIT_FOR_SMALLEST  — BSDs streaming in, waiting for smallest_bsds to finish.
--                          On completion, checks DF field validity. Invalid DF
--                          → immediate IDLE (frees slot in ~1 µs vs ~30 µs).
--                          Non-CRC-managed DFs proceed to a raw iter=0 forward.
--     GEN_FLIP_MASK      — Generate a 112-bit mask with 1s at the bit positions
--                          to flip for the current iteration
--     APPLY_FLIP         — XOR the mask with the original decoded bits
--     START_CRC          — Submit the flipped message to CRC checker
--     CHECK_CRC          — Wait for CRC result:
--                            - CRC good → output message, return to IDLE
--                            - CRC bad, iterations remain → next iteration
--                            - CRC bad, all 32 tried → give up, return to IDLE
--
-- Byte-order swizzle:
--   The smallest_bsds module accumulates bits in transmission order (MSB first),
--   but the CRC module expects bytes in a specific order. The swizzle function
--   reverses the byte order of the 112-bit message to match.
--
-- The DF field (bit 111 of the accumulator) determines message type:
--   DF < 16 (bit 111 = '0') → short message, uses smallest_short rankings
--   DF >= 16 (bit 111 = '1') → extended message, uses smallest_ext rankings
-- =============================================================================

library ieee;
    use ieee.numeric_std.all;
    use ieee.std_logic_1164.all;

library work ;
    use work.smallest_bsds_p.all ;
    use work.adsb_pkg.all;

entity bit_flipper is
  port(
    clock           :   in  std_logic;                         -- System clock
    reset           :   in  std_logic;                         -- Asynchronous reset

    start           :   in  std_logic ;                        -- Begin error correction (pulse)

    busy            :   out std_logic ;                        -- High while brute-force search is active

    in_bsd          :   in  signed(7 downto 0);                -- BSD value from bsd_calculator
    in_bhd          :   in  std_logic;                         -- BHD value from bsd_calculator
    in_valid        :   in  std_logic ;                        -- BSD/BHD valid strobe

    msg_bits        :   out std_logic_vector(111 downto 0) ;   -- Accepted message output (valid when msg_valid='1')
    msg_valid       :   out std_logic;                         -- Message output valid pulse
    dbg_smallest_done : out std_logic;                         -- Pulse when smallest_bsds finishes
    dbg_invalid_df    : out std_logic;                         -- Pulse when DF is rejected before CRC
    dbg_crc_attempt   : out std_logic;                         -- Pulse when a DF11/DF17/DF18 CRC candidate is launched
    dbg_crc_pass      : out std_logic;                         -- Pulse on DF11/DF17/DF18 CRC success
    dbg_crc_exhausted : out std_logic;                         -- Pulse when all 32 DF11/DF17/DF18 CRC tries fail
    dbg_candidate_df4 : out std_logic;                         -- Pulse when assembled candidate classifies as DF4
    dbg_candidate_df5 : out std_logic;                         -- Pulse when assembled candidate classifies as DF5
    dbg_candidate_df11 : out std_logic;                        -- Pulse when assembled candidate classifies as DF11
    dbg_crc0_valid    : out std_logic;                         -- Pulse when iter=0 candidate is captured
    dbg_crc0_w0       : out std_logic_vector(31 downto 0);
    dbg_crc0_w1       : out std_logic_vector(31 downto 0);
    dbg_crc0_w2       : out std_logic_vector(31 downto 0);
    dbg_crc0_w3       : out std_logic_vector(31 downto 0)
  );
end entity;

architecture arch of bit_flipper is

    -- Original decoded bits (from smallest_bsds accumulator)
    signal smallest_bits    :   std_logic_vector(111 downto 0) ;

    -- CRC checker interface
    signal crc_good         :   std_logic ;
    signal crc_valid        :   std_logic ;

    -- 112-bit flip masks: '1' at positions to be flipped
    signal flip_mask_ext    :   std_logic_vector(111 downto 0) ;
    signal flip_mask_short  :   std_logic_vector(111 downto 0) ;

    -- Message after applying the flip mask (XOR)
    signal flipped_ext      :   std_logic_vector(111 downto 0) ;
    signal flipped_short    :   std_logic_vector(111 downto 0) ;

    -- CRC input candidate
    signal flipped_msg      :   std_logic_vector(111 downto 0) ;
    signal flipped_valid    :   std_logic ;

    -- Control signal to clear the smallest_bsds rankings for a new message
    signal smallest_clear   :   std_logic ;

    -- Completion flag from smallest_bsds (all 112 bits received)
    signal smallest_done    :   std_logic ;

    -- Rankings from smallest_bsds
    signal smallest_ext     :   elements_t(0 to 4) ;
    signal smallest_short   :   elements_t(0 to 4) ;

    -- FSM states
    type fsm_t is (IDLE, WAIT_FOR_SMALLEST, GEN_FLIP_MASK, APPLY_FLIP, START_CRC, CHECK_CRC, ACCEPT_MSG) ;
    signal fsm : fsm_t ;

    -- Short message flag: set when DF < 16 (bit 111 = '0')
    -- Selects smallest_short rankings and flipped_short candidate for CRC
    signal is_short : std_logic ;
    signal accept_crc_good : std_logic ;

    -- =========================================================================
    -- valid_df: Check if the Downlink Format field is a known Mode-S format
    --
    -- The DF is the top 5 bits of the first transmitted byte, which sits at
    -- bits(111:107) of the accumulator. Valid DFs per ICAO Annex 10:
    --   Short (56-bit): DF 0, 4, 5, 11
    --   Extended (112-bit): DF 16, 17, 18, 19, 20, 21, 24
    --
    -- Any other DF means this isn't a Mode-S message — abort immediately
    -- rather than wasting ~30 µs on 32 CRC iterations over garbage.
    -- =========================================================================
    function valid_df( bits : std_logic_vector(111 downto 0) ) return boolean is
        variable df : integer range 0 to 31 ;
    begin
        df := to_integer(unsigned(bits(111 downto 107))) ;
        case df is
            when 0 | 4 | 5 | 11 |
                 16 | 17 | 18 | 19 | 20 | 21 | 24 =>
                return true ;
            when others =>
                return false ;
        end case ;
    end function ;

    -- =========================================================================
    -- calculate_flip_mask: Convert a 5-bit iteration counter into a 112-bit mask
    --
    -- Each bit of 'iter' corresponds to one of the 5 weakest bit positions.
    -- If iter(j) = '1', the mask has a '1' at the bit position stored in
    -- smallest(j).index. All other mask bits are '0'.
    --
    -- Example: if the 5 weakest bits are at positions [17, 42, 68, 91, 103]
    -- and iter = "01010", the mask has 1s at positions 42 and 91.
    -- =========================================================================
    function calculate_flip_mask( iter : unsigned ; smallest : elements_t ) return std_logic_vector is
        variable rv : std_logic_vector(111 downto 0) := (others =>'0') ;
    begin
        assert iter'length = 5
            report "Iteration length can only be 5 bits long"
            severity failure ;
        assert smallest'length = 5
            report "Smallest elements length can only be 5 elements"
            severity failure ;
        -- NOTE: smallest(j).index is in transmission order (0 = first bit),
        -- but the bits accumulator in smallest_bsds stores bits with the
        -- first transmitted bit at position 111 (MSB) and the last at
        -- position 0 (LSB). Map from transmission index to accumulator
        -- position using (111 - index).
        for i in rv'range loop
            for j in smallest'range loop
                if( iter(j) = '1' and smallest(j).index = 111 - i ) then
                    rv(i) := '1' ;
                    exit ;
                end if ;
            end loop ;
        end loop ;
        return rv ;
    end function ;

    -- =========================================================================
    -- swizzle: Reverse byte order of a 112-bit (14-byte) message
    --
    -- The BSD accumulator stores bits in transmission order, but the CRC
    -- module processes bytes starting from the first transmitted byte at
    -- the low end of the vector. This function reverses the byte order:
    --   byte 0 (first transmitted) → bits 111:104 in → bits 7:0 out
    --   byte 1 → bits 103:96 in → bits 15:8 out
    --   ... and so on
    -- =========================================================================
    function swizzle( x : std_logic_vector ) return std_logic_vector is
        variable rv : std_logic_vector(111 downto 0) ;
        constant n : integer := 112/8 ;     -- 14 bytes
    begin
        for i in 0 to n-1 loop
            rv((n-i)*8-1 downto (n-i-1)*8) := x((i+1)*8-1 downto i*8) ;
        end loop ;
        return rv ;
    end function ;

    function message_df(bits : std_logic_vector(111 downto 0)) return natural is
    begin
        return to_integer(unsigned(bits(7 downto 3)));
    end function;

    function crc_managed_df(bits : std_logic_vector(111 downto 0)) return boolean is
    begin
        case message_df(bits) is
            when 11 | 17 | 18 =>
                return true;
            when others =>
                return false;
        end case;
    end function;

begin

    -- Pulse the weakest-bit tracker clear when a new message starts.
    -- Keeping this as a normal registered pulse avoids synthesis inferring
    -- strange event-dependent logic from a concurrent assignment.
    clear_smallest : process(clock, reset)
    begin
        if reset = '1' then
            smallest_clear <= '0' ;
        elsif rising_edge(clock) then
            smallest_clear <= start ;
        end if ;
    end process ;

    -- =========================================================================
    -- Sub-component: smallest_bsds — tracks the 5 weakest bits
    -- =========================================================================
    U_smallest : entity work.smallest_bsds
      port map (
        clock           =>  clock,
        reset           =>  reset,

        clear           =>  smallest_clear,

        finished        =>  smallest_done,

        bsd             =>  in_bsd,
        bhd             =>  in_bhd,
        bsd_valid       =>  in_valid,

        bits            =>  smallest_bits,
        smallest_ext    =>  smallest_ext,
        smallest_short  =>  smallest_short
      ) ;

    -- =========================================================================
    -- Sub-component: adsb_crc — validates each candidate message
    -- =========================================================================
    U_crc : entity work.adsb_crc
      port map (
        clock       =>  clock,
        reset       =>  reset,

        busy        =>  open,               -- We track our own busy state

        data        =>  flipped_msg,
        data_valid  =>  flipped_valid,

        crc         =>  open,
        crc_good    =>  crc_good,
        crc_valid   =>  crc_valid
      ) ;

    -- =========================================================================
    -- Diagnostic counter: counts total CRC passes (for simulation debug)
    -- =========================================================================
    good_count : process(clock, reset)
        variable count : integer ;
    begin
        if( reset = '1' ) then
            count := 0 ;
        elsif( rising_edge(clock) ) then
            if( crc_good = '1' and crc_valid = '1' ) then
                count := count + 1 ;
            end if ;
        end if ;
    end process ;

    -- =========================================================================
    -- Brute-force FSM: iterate through all 32 flip combinations
    --
    -- The 5-bit 'iter' variable counts from 0 to 31 (inclusive).
    -- iter = 0 means "flip no bits" (check the original message first).
    -- iter = 1 means "flip only the weakest bit".
    -- iter = 31 means "flip all 5 weakest bits".
    --
    -- Each iteration takes ~15–20 clock cycles (CRC computation time).
    -- Total worst case: 32 × ~15 = ~480 clocks = ~30 µs at 16 MHz.
    -- =========================================================================
    brute_force : process(clock, reset)
        variable iter : unsigned(4 downto 0) := (others =>'0') ;
        variable candidate_msg : std_logic_vector(111 downto 0);
    begin
        if( reset = '1' ) then
            fsm <= IDLE ;
            is_short <= '0' ;
            flip_mask_ext <= (others => '0') ;
            flip_mask_short <= (others => '0') ;
            flipped_ext <= (others => '0') ;
            flipped_short <= (others => '0') ;
            flipped_msg <= (others => '0') ;
            flipped_valid <= '0' ;
            msg_bits <= (others =>'0') ;
            msg_valid <= '0' ;
            dbg_smallest_done <= '0' ;
            dbg_invalid_df <= '0' ;
            dbg_crc_attempt <= '0' ;
            dbg_crc_pass <= '0' ;
            dbg_crc_exhausted <= '0' ;
            dbg_candidate_df4 <= '0' ;
            dbg_candidate_df5 <= '0' ;
            dbg_candidate_df11 <= '0' ;
            accept_crc_good <= '0' ;
            dbg_crc0_valid <= '0' ;
            dbg_crc0_w0 <= (others => '0') ;
            dbg_crc0_w1 <= (others => '0') ;
            dbg_crc0_w2 <= (others => '0') ;
            dbg_crc0_w3 <= (others => '0') ;
            busy <= '0' ;
        elsif( rising_edge(clock) ) then
            msg_valid <= '0' ;
            flipped_valid <= '0' ;
            dbg_smallest_done <= '0' ;
            dbg_invalid_df <= '0' ;
            dbg_crc_attempt <= '0' ;
            dbg_crc_pass <= '0' ;
            dbg_crc_exhausted <= '0' ;
            dbg_candidate_df4 <= '0' ;
            dbg_candidate_df5 <= '0' ;
            dbg_candidate_df11 <= '0' ;
            accept_crc_good <= '0' ;
            dbg_crc0_valid <= '0' ;
            case fsm is
                when IDLE =>
                    busy <= '0' ;
                    iter := (others =>'0') ;
                    if( start = '1' ) then
                        fsm <= WAIT_FOR_SMALLEST ;
                        busy <= '1' ;
                    end if ;

                when WAIT_FOR_SMALLEST =>
                    -- Wait for all 112 BSDs to arrive and rankings to complete
                    if( smallest_done = '1' ) then
                        dbg_smallest_done <= '1' ;
                        -- Determine message type: DF < 16 → short (56-bit)
                        -- Bit 111 is MSB of DF field; '0' means DF 0-15 (short)
                        is_short <= not smallest_bits(111) ;
                        -- Early exit: check DF field before spending ~30 µs on CRC
                        if valid_df(smallest_bits) then
                            case to_integer(unsigned(smallest_bits(111 downto 107))) is
                                when 4 =>
                                    dbg_candidate_df4 <= '1';
                                when 5 =>
                                    dbg_candidate_df5 <= '1';
                                when 11 =>
                                    dbg_candidate_df11 <= '1';
                                when others =>
                                    null;
                            end case;
                            fsm <= GEN_FLIP_MASK ;
                        else
                            -- Invalid DF — not a Mode-S message, free decoder immediately
                            dbg_invalid_df <= '1' ;
                            fsm <= IDLE ;
                            -- synthesis translate_off
                            report "FLIP_DBG: Invalid DF=" &
                                   integer'image(to_integer(unsigned(smallest_bits(111 downto 107)))) &
                                   ", skipping CRC";
                            -- synthesis translate_on
                        end if ;
                        -- synthesis translate_off
                        report "FLIP_DBG: BSDs done. DF=" &
                               integer'image(to_integer(unsigned(smallest_bits(111 downto 107)))) &
                               " bytes:" &
                               " " & integer'image(to_integer(unsigned(smallest_bits(111 downto 104)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(103 downto 96)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(95 downto 88)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(87 downto 80)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(79 downto 72)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(71 downto 64)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(63 downto 56)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(55 downto 48)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(47 downto 40)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(39 downto 32)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(31 downto 24)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(23 downto 16)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(15 downto 8)))) &
                               " " & integer'image(to_integer(unsigned(smallest_bits(7 downto 0))));
                        -- synthesis translate_on
                    end if ;

                when GEN_FLIP_MASK =>
                    -- Generate 112-bit flip mask for current iteration,
                    -- using the appropriate ranking for the message type
                    if is_short = '1' then
                        flip_mask_short <= calculate_flip_mask( iter, smallest_short ) ;
                    else
                        flip_mask_ext <= calculate_flip_mask( iter, smallest_ext ) ;
                    end if ;
                    fsm <= APPLY_FLIP ;

                when APPLY_FLIP =>
                    -- XOR the original bits with the appropriate flip mask
                    if is_short = '1' then
                        flipped_short <= smallest_bits xor flip_mask_short ;
                    else
                        flipped_ext <= smallest_bits xor flip_mask_ext ;
                    end if ;
                    fsm <= START_CRC ;

                when START_CRC =>
                    -- Present the iter candidate in the byte order used by the
                    -- rest of the pipeline. Only DF11/DF17/DF18 enter the CRC
                    -- / FEC path; all other valid DFs are forwarded raw at
                    -- iter=0 and filtered in host software if needed.
                    if is_short = '1' then
                        candidate_msg := swizzle(flipped_short);
                    else
                        candidate_msg := swizzle(flipped_ext);
                    end if ;
                    flipped_msg <= candidate_msg;
                    if crc_managed_df(candidate_msg) then
                        flipped_valid <= '1' ;
                        dbg_crc_attempt <= '1' ;
                        if iter = 0 then
                            dbg_crc0_valid <= '1' ;
                            dbg_crc0_w0 <= candidate_msg(31 downto 0);
                            dbg_crc0_w1 <= candidate_msg(63 downto 32);
                            dbg_crc0_w2 <= candidate_msg(95 downto 64);
                            dbg_crc0_w3 <= x"0000" & candidate_msg(111 downto 96);
                            -- synthesis translate_off
                            report "FLIP_DBG: CRC0 raw DF=" &
                                   integer'image(to_integer(unsigned(candidate_msg(7 downto 3)))) &
                                   " swz_b0=" & integer'image(to_integer(unsigned(candidate_msg(7 downto 0)))) &
                                   " swz_b1=" & integer'image(to_integer(unsigned(candidate_msg(15 downto 8))));
                            -- synthesis translate_on
                        end if ;
                        fsm <= CHECK_CRC ;
                    else
                        fsm <= ACCEPT_MSG ;
                    end if ;

                when CHECK_CRC =>
                    flipped_valid <= '0' ;
                    if( crc_valid = '1' ) then
                        if crc_good = '1' then
                            accept_crc_good <= '1' ;
                            fsm <= ACCEPT_MSG ;
                            -- synthesis translate_off
                            report "FLIP_DBG: *** CRC PASS on iter " &
                                   integer'image(to_integer(iter));
                            -- synthesis translate_on
                        else
                            -- CRC failed — try next combination if iterations remain
                            if( iter < 31 ) then
                                iter := iter + 1 ;
                                fsm <= GEN_FLIP_MASK ;
                            else
                                -- All 32 combinations exhausted — message unrecoverable
                                dbg_crc_exhausted <= '1' ;
                                -- synthesis translate_off
                                report "FLIP_DBG: All 32 iterations failed, giving up";
                                -- synthesis translate_on
                                fsm <= IDLE ;
                            end if ;
                        end if ;
                    end if ;

                when ACCEPT_MSG =>
                    msg_bits <= flipped_msg ;
                    msg_valid <= '1' ;
                    dbg_crc_pass <= accept_crc_good ;
                    fsm <= IDLE ;

                when others =>
                    fsm <= IDLE ;

            end case ;
        end if ;
    end process ;

end architecture;
