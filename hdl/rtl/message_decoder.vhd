-- =============================================================================
-- message_decoder.vhd — Single Message Decoder Instance
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with timestamp (TOA) passthrough added
-- for MLAT support.
--
-- This module is a single decoder "slot" in the multi-decoder ("Gold Mode")
-- architecture. The top-level adsb_decoder instantiates NUM_DECODERS (typically
-- 8) of these in parallel, allowing simultaneous decoding of overlapping
-- Mode-S transmissions.
--
-- Each instance wraps two sub-components in a pipeline:
--
--   power samples → [bsd_calculator] → BSD stream → [bit_flipper] → decoded message
--                                                    (contains smallest_bsds + CRC)
--
-- Lifecycle of a single message:
--   1. Preamble detector asserts SOM (Start-Of-Message) for this decoder
--   2. This module latches the RPL and TOA, starts accepting power samples
--   3. bsd_calculator converts 16 samples/bit into soft decisions (BSD + BHD)
--   4. bit_flipper accumulates BSDs, identifies weak bits, and only runs the
--      brute-force CRC/FEC path for DF11/DF17/DF18. Other valid DFs are
--      forwarded raw at iter=0 for host-side filtering.
--   5. On an accepted candidate: msg_bits + msg_valid are asserted
--   6. Module returns to idle (busy deasserts)
--
-- The 'active' flag and downcount mechanism gate power sample forwarding:
--   - On SOM: latch RPL and TOA, start downcounting from EXTENDED_MESSAGE_LENGTH
--   - While active: forward power samples to bsd_calculator
--   - When downcount expires: stop forwarding (message fully received)
--   - If SOM arrives while already active: ignore it (message in progress)
--
-- EXTENDED_MESSAGE_LENGTH is sized for the worst case (112-bit extended message).
-- Short messages finish processing earlier but the extra samples are harmless.
--
-- The 'busy' output reflects the bit_flipper's busy state (not just 'active'),
-- because the post-sample accept/search work continues after all samples have
-- been received. The preamble detector checks this before assigning new
-- messages.
-- =============================================================================

library ieee;
    use ieee.numeric_std.all;
    use ieee.std_logic_1164.all;

library work ;
    use work.adsb_pkg.all ;

entity message_decoder is
  port(
    clock       :   in  std_logic;                                      -- System clock
    reset       :   in  std_logic;                                      -- Asynchronous reset

    busy        :   out std_logic ;                                     -- High while decoding (includes post-sample accept/search)

    power_in    :   in  signed(INPUT_POWER_WIDTH-1 downto 0);           -- Power samples from preamble detector
    rpl_in      :   in  signed(INPUT_POWER_WIDTH-1 downto 0);           -- Reference Power Level for this message
    som         :   in  std_logic;                                      -- Start-Of-Message pulse (from preamble detector)
    in_valid    :   in  std_logic;                                      -- Input valid strobe

    toa_in      :   in  unsigned(COUNTER_WIDTH-1 downto 0);             -- Time-Of-Arrival from preamble detector
    toa_out     :   out unsigned(COUNTER_WIDTH-1 downto 0);             -- Latched TOA for this message

    rpl_out     :   out signed(INPUT_POWER_WIDTH-1 downto 0);           -- Latched RPL for this message

    msg_bits    :   out std_logic_vector(111 downto 0) ;                -- Accepted message (CRC-checked for DF11/17/18 only)
    msg_valid   :   out std_logic;                                      -- Message valid pulse
    dbg_smallest_done : out std_logic;
    dbg_invalid_df    : out std_logic;
    dbg_crc_attempt   : out std_logic;
    dbg_crc_pass      : out std_logic;
    dbg_crc_exhausted : out std_logic;
    dbg_candidate_df4 : out std_logic;
    dbg_candidate_df5 : out std_logic;
    dbg_candidate_df11 : out std_logic;
    dbg_crc0_valid    : out std_logic;
    dbg_crc0_w0       : out std_logic_vector(31 downto 0);
    dbg_crc0_w1       : out std_logic_vector(31 downto 0);
    dbg_crc0_w2       : out std_logic_vector(31 downto 0);
    dbg_crc0_w3       : out std_logic_vector(31 downto 0)
  );
end entity;


architecture arch of message_decoder is

    -- Use the shared constant from adsb_pkg (2240 samples = 140 µs).
    -- See adsb_pkg.vhd for derivation.

    -- Registered RPL (latched on SOM)
    signal rpl_reg                      :   signed(INPUT_POWER_WIDTH-1 downto 0);
    signal rpl_reg_valid                :   std_logic;

    -- BSD calculator outputs
    signal bsd                          :   signed(7 downto 0);
    signal bhd                          :   std_logic;
    signal bsd_valid                    :   std_logic;

    -- Power sample pipeline (one stage of delay for timing alignment)
    signal power                        :   signed(INPUT_POWER_WIDTH-1 downto 0);
    signal power_valid                  :   std_logic;
    signal power_delay                  :   signed(INPUT_POWER_WIDTH-1 downto 0);
    signal power_delay_valid            :   std_logic;

    -- Active flag: high while this decoder is receiving samples for a message
    signal active                       :   std_logic;

    -- Byte accumulation signals (unused in current implementation but kept
    -- for potential future byte-level output)
    signal accum_byte                   :   std_logic_vector(7 downto 0);
    signal byte_reg                     :   std_logic_vector(7 downto 0);
    signal byte_reg_valid               :   std_logic;

    -- Bit flipper interface
    signal flipper_busy                 :   std_logic;
    signal flipper_bits                 :   std_logic_vector(111 downto 0) ;
    signal flipper_valid                :   std_logic ;
    signal flipper_smallest_done        :   std_logic ;
    signal flipper_invalid_df           :   std_logic ;
    signal flipper_crc_attempt          :   std_logic ;
    signal flipper_crc_pass             :   std_logic ;
    signal flipper_crc_exhausted        :   std_logic ;
    signal flipper_candidate_df4        :   std_logic ;
    signal flipper_candidate_df5        :   std_logic ;
    signal flipper_candidate_df11       :   std_logic ;
    signal flipper_crc0_valid           :   std_logic ;
    signal flipper_crc0_w0              :   std_logic_vector(31 downto 0) ;
    signal flipper_crc0_w1              :   std_logic_vector(31 downto 0) ;
    signal flipper_crc0_w2              :   std_logic_vector(31 downto 0) ;
    signal flipper_crc0_w3              :   std_logic_vector(31 downto 0) ;

    -- Timestamp register: latched on SOM, held until next message
    signal toa_reg                      :   unsigned(COUNTER_WIDTH-1 downto 0);

begin

    -- The decoder is "busy" as long as the bit_flipper is working.
    -- This includes both the sample-receiving phase and the brute-force
    -- CRC search phase that follows.
    busy <= flipper_busy ;

    -- Pass through the bit_flipper's output directly
    msg_bits <= flipper_bits ;
    msg_valid <= flipper_valid ;
    dbg_smallest_done <= flipper_smallest_done;
    dbg_invalid_df <= flipper_invalid_df;
    dbg_crc_attempt <= flipper_crc_attempt;
    dbg_crc_pass <= flipper_crc_pass;
    dbg_crc_exhausted <= flipper_crc_exhausted;
    dbg_candidate_df4 <= flipper_candidate_df4;
    dbg_candidate_df5 <= flipper_candidate_df5;
    dbg_candidate_df11 <= flipper_candidate_df11;
    dbg_crc0_valid <= flipper_crc0_valid;
    dbg_crc0_w0 <= flipper_crc0_w0;
    dbg_crc0_w1 <= flipper_crc0_w1;
    dbg_crc0_w2 <= flipper_crc0_w2;
    dbg_crc0_w3 <= flipper_crc0_w3;

    -- Output the latched timestamp and RPL for this message
    toa_out <= toa_reg ;
    rpl_out <= rpl_reg ;

    -- =========================================================================
    -- Input gating and sample forwarding
    --
    -- This process controls when power samples are forwarded to the BSD
    -- calculator. It implements:
    --   - SOM latching: On SOM pulse, latch RPL and TOA, set active
    --   - Sample gating: Only forward samples while active
    --   - Timeout: Count down from EXTENDED_MESSAGE_LENGTH to auto-deactivate
    --   - One-stage delay: power_delay/power_delay_valid pipeline stage
    -- =========================================================================
    delay_input : process(clock,reset)
        variable downcount : integer range 0 to (EXTENDED_MESSAGE_LENGTH);
        variable ignored : integer ;   -- Count of SOM pulses ignored while busy
    begin
        if(reset = '1') then
            power_valid <= '0';
            active <= '0';
            rpl_reg_valid <= '0';
            ignored := 0 ;
            toa_reg <= (others => '0');
        elsif rising_edge(clock) then

            -- Default: no RPL update, no power forwarding
            rpl_reg_valid <= '0';
            power_valid <= '0';

            -- Pipeline delay stage (aligns power with BSD calculator timing)
            power_delay <= power;
            power_delay_valid <= power_valid;

            -- Forward power samples while SOM is active or this decoder is active
            if(som = '1') or (active = '1') then
                power <= power_in;
                power_valid <= in_valid;
            end if;

            -- Sample countdown: when all expected samples have been received,
            -- deactivate this decoder slot. Decrement on in_valid only so the
            -- count tracks actual samples, not raw clock cycles (the core clock
            -- is faster than the sample rate after the clock domain move to
            -- S_AXI_ACLK).
            if active = '1' and in_valid = '1' then
                if downcount > 0 then
                    downcount := downcount - 1;
                else
                    active <= '0';
                end if;
            end if;

            -- SOM handling
            if som ='1' and active = '0' then
                -- New message: latch RPL and TOA, activate
                rpl_reg_valid <= '1';
                rpl_reg <= rpl_in;
                toa_reg <= toa_in;              -- Capture Time-Of-Arrival for MLAT
                active <= '1';
                downcount := EXTENDED_MESSAGE_LENGTH;
                -- synthesis translate_off
                report "MDEC_DBG: SOM accepted, rpl=" &
                       integer'image(to_integer(rpl_in)) &
                       " power_in=" & integer'image(to_integer(power_in));
                -- synthesis translate_on
            elsif som = '1' and active = '1' then
                -- Already processing a message — ignore this SOM
                ignored := ignored + 1 ;
            end if;

        end if;
    end process;

    -- =========================================================================
    -- Sub-component: bsd_calculator
    -- Converts the gated power sample stream into BSD/BHD pairs.
    -- Fed from the one-stage-delayed power signal for timing alignment.
    -- =========================================================================
    U_bsd_calculator : entity work.bsd_calculator
      port map(
        clock           =>  clock,
        reset           =>  reset,

        power_in        =>  power_delay,
        power_in_valid  =>  power_delay_valid,

        rpl_in          =>  rpl_reg,
        rpl_valid       =>  rpl_reg_valid,

        bsd             =>  bsd,
        bhd             =>  bhd,
        out_valid       =>  bsd_valid
      );

    -- =========================================================================
    -- Sub-component: bit_flipper
    -- Accumulates BSDs, identifies weak bits, runs CRC/FEC for DF11/17/18,
    -- and forwards other valid DFs raw. Triggered by rpl_reg_valid.
    -- =========================================================================
    U_bit_flipper : entity work.bit_flipper
      port map (
        clock       =>  clock,
        reset       =>  reset,

        start       =>  rpl_reg_valid,       -- Start when new message begins

        busy        =>  flipper_busy,

        in_bsd      =>  bsd,
        in_bhd      =>  bhd,
        in_valid    =>  bsd_valid,

        msg_bits    =>  flipper_bits,
        msg_valid   =>  flipper_valid,
        dbg_smallest_done => flipper_smallest_done,
        dbg_invalid_df    => flipper_invalid_df,
        dbg_crc_attempt   => flipper_crc_attempt,
        dbg_crc_pass      => flipper_crc_pass,
        dbg_crc_exhausted => flipper_crc_exhausted,
        dbg_candidate_df4 => flipper_candidate_df4,
        dbg_candidate_df5 => flipper_candidate_df5,
        dbg_candidate_df11 => flipper_candidate_df11,
        dbg_crc0_valid    => flipper_crc0_valid,
        dbg_crc0_w0       => flipper_crc0_w0,
        dbg_crc0_w1       => flipper_crc0_w1,
        dbg_crc0_w2       => flipper_crc0_w2,
        dbg_crc0_w3       => flipper_crc0_w3
      ) ;

end architecture;
