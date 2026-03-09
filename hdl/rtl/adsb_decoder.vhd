-- =============================================================================
-- adsb_decoder.vhd — Top-Level ADS-B / Mode-S Decode Pipeline
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with timestamp counter input added for
-- MLAT (Multilateration) support.
--
-- This is the top-level module that wires together the full decode pipeline:
--
--   in_power (I²+Q²) → [edge_detector] → [preamble_detector] → [N × message_decoder]
--                                                                        ↓
--                                                              decoded messages + valid
--
-- Signal flow:
--   1. adsb_edge_detector: Detects rising edges in the power signal using
--      a sliding-window 3-point comparison with threshold qualification.
--      Output: delayed power + edge flags.
--
--   2. preamble_detector: Correlates edge pattern against the known Mode-S
--      preamble (4 pulses at 0, 1, 3.5, 4.5 µs). When correlation passes,
--      assigns the message to an available decoder via round-robin SOM.
--      Latches timestamp (TOA) and estimates Reference Power Level (RPL).
--
--   3. message_decoder (×N): Each instance independently demodulates its
--      assigned message: power → BSD calculator → bit flipper. DF11/DF17/DF18
--      use CRC/FEC in hardware; other valid DFs are forwarded raw for
--      host-side filtering.
--
-- The generate_decoders block creates NUM_DECODERS parallel instances
-- (default 8 = "Gold Mode"). This allows decoding up to 8 overlapping
-- Mode-S transmissions simultaneously, critical in busy airspace.
--
-- Timestamp path:
--   counter_value (from timestamp_counter) → preamble_detector → det_toa
--   → message_decoder(i).toa_in. Each decoder latches the TOA when it
--   accepts a message. The latched TOA is available on out_toas(i),
--   valid when out_valid(i) pulses.
--
-- Diagnostic: check_crc_valid process counts successful CRC passes per
-- decoder during simulation, useful for verifying decode performance.
-- =============================================================================

library ieee ;
    use ieee.std_logic_1164.all ;
    use ieee.numeric_std.all ;

library work ;
    use work.adsb_pkg.all ;

entity adsb_decoder is
  generic (
    NUM_DECODERS    :       positive    := 8;                          -- Number of parallel decoder instances
    PREAMBLE_MESSAGE_DELAY : integer := 50;
    PREAMBLE_OUTPUT_TAP    : integer := 76;
    ENABLE_DEEP_DEBUG      : boolean := false
  );
  port (
    clock           :   in  std_logic ;                                -- System clock (16 MHz from PLL)
    reset           :   in  std_logic ;                                -- Synchronous reset

    init            :   in  std_logic ;                                -- Initialisation signal (passed to edge detector)

    in_power        :   in  signed(INPUT_POWER_WIDTH-1 downto 0) ;     -- Input power (I²+Q²) from RF frontend
    in_valid        :   in  std_logic ;                                -- Input valid strobe

    counter_value   :   in  unsigned(COUNTER_WIDTH-1 downto 0) ;       -- Free-running timestamp counter (for MLAT)
    quiet_score_shift_cfg : in unsigned(2 downto 0);
    snr_ratio_shift_cfg   : in unsigned(2 downto 0);
    holdoff_cfg           : in unsigned(11 downto 0);

    debug_rpl       :   out signed(INPUT_POWER_WIDTH-1 downto 0) ;     -- Debug: current RPL from preamble detector
    debug_edge_count :  out unsigned(31 downto 0) ;
    debug_som_count  :  out unsigned(31 downto 0) ;
    debug_msg_count  :  out unsigned(31 downto 0) ;
    debug_edge_shape_count : out unsigned(31 downto 0) ;
    debug_edge_qual_count  : out unsigned(31 downto 0) ;
    debug_preamble_pass_count : out unsigned(31 downto 0) ;
    debug_preamble_detect_count : out unsigned(31 downto 0) ;
    debug_preamble_abs_count : out unsigned(31 downto 0) ;
    debug_preamble_quiet_count : out unsigned(31 downto 0) ;
    debug_preamble_quiet_a_fail_count : out unsigned(31 downto 0) ;
    debug_preamble_quiet_b_fail_count : out unsigned(31 downto 0) ;
    debug_preamble_quiet_c_fail_count : out unsigned(31 downto 0) ;
    debug_preamble_quiet_d_fail_count : out unsigned(31 downto 0) ;
    debug_preamble_snr_count : out unsigned(31 downto 0) ;
    debug_preamble_holdoff_count : out unsigned(31 downto 0) ;
    debug_preamble_peak_age : out unsigned(31 downto 0) ;
    debug_preamble_no_free_count : out unsigned(31 downto 0) ;
    debug_preamble_busy_drop_count : out unsigned(31 downto 0) ;
    debug_decoder_busy_max : out unsigned(31 downto 0) ;
    debug_smallest_done_count : out unsigned(31 downto 0) ;
    debug_invalid_df_count : out unsigned(31 downto 0) ;
    debug_crc_attempt_count : out unsigned(31 downto 0) ;
    debug_crc_pass_count : out unsigned(31 downto 0) ;
    debug_crc_exhausted_count : out unsigned(31 downto 0) ;
    debug_df4_count : out unsigned(31 downto 0) ;
    debug_df5_count : out unsigned(31 downto 0) ;
    debug_df11_count : out unsigned(31 downto 0) ;
    debug_cand_df4_count : out unsigned(31 downto 0) ;
    debug_cand_df5_count : out unsigned(31 downto 0) ;
    debug_cand_df11_count : out unsigned(31 downto 0) ;
    debug_df17_count : out unsigned(31 downto 0) ;
    debug_df18_count : out unsigned(31 downto 0) ;
    debug_crc0_w0 : out std_logic_vector(31 downto 0);
    debug_crc0_w1 : out std_logic_vector(31 downto 0);
    debug_crc0_w2 : out std_logic_vector(31 downto 0);
    debug_crc0_w3 : out std_logic_vector(31 downto 0);

    out_messages    :   out messages_t(NUM_DECODERS-1 downto 0) ;      -- Decoded messages from all decoders
    out_valid       :   out std_logic_vector(NUM_DECODERS-1 downto 0) ;-- Per-decoder message valid flags

    out_toas        :   out toas_t(NUM_DECODERS-1 downto 0) ;          -- Per-decoder Time-Of-Arrival (latched on SOM)

    out_rpls        :   out rpls_t(NUM_DECODERS-1 downto 0)            -- Per-decoder Reference Power Level
  ) ;
end entity ;

architecture arch of adsb_decoder is
    type words32_t is array(natural range <>) of std_logic_vector(31 downto 0);

    -- Internal signals between pipeline stages
    signal edge_out_power   :   signed(INPUT_POWER_WIDTH-1 downto 0) ;   -- Edge detector → preamble detector: power
    signal edge_out_level   :   std_logic ;                               -- Edge detector → preamble detector: edge flag
    signal edge_out_valid   :   std_logic ;                               -- Edge detector → preamble detector: valid

    signal det_power        :   signed(INPUT_POWER_WIDTH-1 downto 0) ;   -- Preamble detector → decoders: power
    signal det_valid        :   std_logic ;                               -- Preamble detector → decoders: valid

    signal det_som          :   std_logic_vector(NUM_DECODERS-1 downto 0) ;  -- Per-decoder Start-Of-Message
    signal det_rpl          :   signed(INPUT_POWER_WIDTH-1 downto 0) ;       -- Reference Power Level for current message

    signal decoder_busy     :   std_logic_vector(NUM_DECODERS-1 downto 0) ;  -- Per-decoder busy flags (fed back to preamble det.)

    signal msgs_decoded     :   messages_t(NUM_DECODERS-1 downto 0);     -- Decoded messages from each decoder
    signal msgs_valid       :   std_logic_vector(NUM_DECODERS-1 downto 0);  -- Per-decoder valid flags
    signal decoder_toas     :   toas_t(NUM_DECODERS-1 downto 0);          -- Per-decoder latched TOA
    signal decoder_rpls     :   rpls_t(NUM_DECODERS-1 downto 0);          -- Per-decoder latched RPL

    signal det_toa          :   unsigned(COUNTER_WIDTH-1 downto 0) ;     -- Time-Of-Arrival from preamble detector
    signal debug_edge_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_som_count_i  : unsigned(31 downto 0) := (others => '0');
    signal debug_msg_count_i  : unsigned(31 downto 0) := (others => '0');
    signal debug_edge_shape_count_i : unsigned(31 downto 0);
    signal debug_edge_qual_count_i  : unsigned(31 downto 0);
    signal debug_preamble_pass_count_i : unsigned(31 downto 0);
    signal debug_preamble_detect_count_i : unsigned(31 downto 0);
    signal debug_preamble_abs_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_a_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_b_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_c_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_d_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_snr_count_i : unsigned(31 downto 0);
    signal debug_preamble_holdoff_count_i : unsigned(31 downto 0);
    signal debug_preamble_peak_age_i : unsigned(31 downto 0);
    signal debug_preamble_no_free_count_i : unsigned(31 downto 0);
    signal debug_preamble_busy_drop_count_i : unsigned(31 downto 0);
    signal debug_decoder_busy_max_i : unsigned(31 downto 0) := (others => '0');
    signal debug_smallest_done_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_invalid_df_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_crc_attempt_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_crc_pass_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_crc_exhausted_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_df4_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_df5_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_df11_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_cand_df4_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_cand_df5_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_cand_df11_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_df17_count_i : unsigned(31 downto 0) := (others => '0');
    signal debug_df18_count_i : unsigned(31 downto 0) := (others => '0');
    signal decoder_smallest_done : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_invalid_df : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_crc_attempt : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_crc_pass : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_crc_exhausted : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_candidate_df4 : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_candidate_df5 : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_candidate_df11 : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_crc0_valid : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal decoder_crc0_w0 : words32_t(NUM_DECODERS-1 downto 0);
    signal decoder_crc0_w1 : words32_t(NUM_DECODERS-1 downto 0);
    signal decoder_crc0_w2 : words32_t(NUM_DECODERS-1 downto 0);
    signal decoder_crc0_w3 : words32_t(NUM_DECODERS-1 downto 0);
    signal debug_crc0_w0_i : std_logic_vector(31 downto 0) := (others => '0');
    signal debug_crc0_w1_i : std_logic_vector(31 downto 0) := (others => '0');
    function popcount(v : std_logic_vector) return natural is
        variable n : natural := 0;
    begin
        for i in v'range loop
            if v(i) = '1' then
                n := n + 1;
            end if;
        end loop;
        return n;
    end function;

    function message_df(msg : std_logic_vector(111 downto 0)) return natural is
    begin
        return to_integer(unsigned(msg(7 downto 3)));
    end function;

    signal debug_crc0_w2_i : std_logic_vector(31 downto 0) := (others => '0');
    signal debug_crc0_w3_i : std_logic_vector(31 downto 0) := (others => '0');

begin

    -- =========================================================================
    -- Stage 1: Edge Detector
    -- Detects rising edges in the power signal with threshold qualification.
    -- Adds pipeline latency but provides clean edge flags for correlation.
    -- =========================================================================
    U_adsb_edge_detector : entity work.adsb_edge_detector
      generic map (
        ENABLE_DEEP_DEBUG => ENABLE_DEEP_DEBUG
      )
      port map (
        clock       =>  clock,
        reset       =>  reset,

        init        =>  init,

        power_in    =>  in_power,
        in_valid    =>  in_valid,

        power_out   =>  edge_out_power,
        edge_out    =>  edge_out_level,
        out_valid   =>  edge_out_valid,
        debug_edge_shape_count => debug_edge_shape_count_i,
        debug_edge_qual_count  => debug_edge_qual_count_i
      ) ;

    -- =========================================================================
    -- Stage 2: Preamble Detector
    -- Correlates against the Mode-S preamble pattern using sliding-window
    -- power sums at 4 pulse positions. Manages decoder assignment via
    -- round-robin. Captures TOA timestamp on preamble detection.
    -- =========================================================================
    U_preamble_detector : entity work.preamble_detector
      generic map (
        NUM_MESSAGE_DECODER =>  NUM_DECODERS,
        MESSAGE_DELAY_G     =>  PREAMBLE_MESSAGE_DELAY,
        OUTPUT_TAP_G        =>  PREAMBLE_OUTPUT_TAP,
        ENABLE_DEEP_DEBUG   =>  ENABLE_DEEP_DEBUG
      ) port map (
        clock           =>  clock,
        reset           =>  reset,

        power_in        =>  edge_out_power,
        edge_in         =>  edge_out_level,
        in_valid        =>  edge_out_valid,

        decoder_busy    =>  decoder_busy,       -- Feedback from decoders

        counter_value   =>  counter_value,       -- Timestamp for TOA capture
        quiet_score_shift_cfg => quiet_score_shift_cfg,
        snr_ratio_shift_cfg   => snr_ratio_shift_cfg,
        holdoff_cfg           => holdoff_cfg,

        power_out       =>  det_power,
        out_valid       =>  det_valid,
        som             =>  det_som,
        rpl             =>  det_rpl,
        toa_out         =>  det_toa,
        debug_pass_count => debug_preamble_pass_count_i,
        debug_detect_count => debug_preamble_detect_count_i,
        debug_abs_gate_count => debug_preamble_abs_count_i,
        debug_quiet_gate_count => debug_preamble_quiet_count_i,
        debug_quiet_a_fail_count => debug_preamble_quiet_a_fail_count_i,
        debug_quiet_b_fail_count => debug_preamble_quiet_b_fail_count_i,
        debug_quiet_c_fail_count => debug_preamble_quiet_c_fail_count_i,
        debug_quiet_d_fail_count => debug_preamble_quiet_d_fail_count_i,
        debug_snr_gate_count => debug_preamble_snr_count_i,
        debug_holdoff_count => debug_preamble_holdoff_count_i,
        debug_peak_age => debug_preamble_peak_age_i,
        debug_no_free_count => debug_preamble_no_free_count_i,
        debug_busy_drop_count => debug_preamble_busy_drop_count_i
      ) ;

    -- =========================================================================
    -- Stage 3: Parallel Message Decoders (×NUM_DECODERS)
    --
    -- Each decoder instance independently processes one message at a time:
    --   power → BSD calculator → bit flipper → accepted output
    --
    -- All decoders share the same power/valid bus from the preamble detector.
    -- Only the decoder whose SOM bit is asserted will capture samples.
    --
    -- Each decoder's latched TOA is routed to out_toas for MLAT support.
    -- =========================================================================
    generate_decoders : for i in 0 to NUM_DECODERS-1 generate
        U_message_decoder : entity work.message_decoder
          port map (
            clock       =>  clock,
            reset       =>  reset,

            busy        =>  decoder_busy(i),       -- Fed back to preamble detector

            power_in    =>  det_power,
            rpl_in      =>  det_rpl,
            som         =>  det_som(i),            -- Only this decoder's SOM bit
            in_valid    =>  det_valid,

            toa_in      =>  det_toa,               -- Shared TOA from preamble detector
            toa_out     =>  decoder_toas(i),       -- Latched TOA for this message

            rpl_out     =>  decoder_rpls(i),       -- Latched RPL for this message

            msg_bits    =>  msgs_decoded(i),
            msg_valid   =>  msgs_valid(i),
            dbg_smallest_done => decoder_smallest_done(i),
            dbg_invalid_df    => decoder_invalid_df(i),
            dbg_crc_attempt   => decoder_crc_attempt(i),
            dbg_crc_pass      => decoder_crc_pass(i),
            dbg_crc_exhausted => decoder_crc_exhausted(i),
            dbg_candidate_df4 => decoder_candidate_df4(i),
            dbg_candidate_df5 => decoder_candidate_df5(i),
            dbg_candidate_df11 => decoder_candidate_df11(i),
            dbg_crc0_valid    => decoder_crc0_valid(i),
            dbg_crc0_w0       => decoder_crc0_w0(i),
            dbg_crc0_w1       => decoder_crc0_w1(i),
            dbg_crc0_w2       => decoder_crc0_w2(i),
            dbg_crc0_w3       => decoder_crc0_w3(i)
          ) ;
    end generate;

    -- =========================================================================
    -- Diagnostic: CRC pass counter (simulation only)
    --
    -- Counts total successful CRC validations per decoder and overall.
    -- Useful during simulation to verify decode rates and confirm that
    -- messages are being correctly distributed across decoders.
    -- =========================================================================
    check_crc_valid : process(clock, reset)
        type integers_t is array(natural range <>) of integer ;
        variable passes : integers_t(NUM_DECODERS-1 downto 0) := (others => 0) ;
        variable total_passes : integer := 0 ;
    begin
        if( rising_edge(clock) ) then
            for i in 0 to NUM_DECODERS-1 loop
                if( msgs_valid(i) = '1' ) then
                    passes(i) := passes(i) + 1 ;
                    total_passes := total_passes + 1 ;
                end if ;
            end loop;
        end if ;
    end process ;

    debug_counts : process(clock)
        variable busy_now : natural;
        variable df4_hits : natural;
        variable df5_hits : natural;
        variable df11_hits : natural;
        variable df17_hits : natural;
        variable df18_hits : natural;
    begin
        if rising_edge(clock) then
            if reset = '1' then
                debug_edge_count_i <= (others => '0');
                debug_som_count_i <= (others => '0');
                debug_msg_count_i <= (others => '0');
                debug_invalid_df_count_i <= (others => '0');
                debug_crc_pass_count_i <= (others => '0');
                debug_df4_count_i <= (others => '0');
                debug_df5_count_i <= (others => '0');
                debug_df11_count_i <= (others => '0');
                debug_df17_count_i <= (others => '0');
                debug_df18_count_i <= (others => '0');
                if ENABLE_DEEP_DEBUG then
                    debug_smallest_done_count_i <= (others => '0');
                    debug_crc_attempt_count_i <= (others => '0');
                    debug_crc_exhausted_count_i <= (others => '0');
                    debug_cand_df4_count_i <= (others => '0');
                    debug_cand_df5_count_i <= (others => '0');
                    debug_cand_df11_count_i <= (others => '0');
                    debug_decoder_busy_max_i <= (others => '0');
                end if;
            else
                df4_hits := 0;
                df5_hits := 0;
                df11_hits := 0;
                df17_hits := 0;
                df18_hits := 0;

                if edge_out_valid = '1' and edge_out_level = '1' then
                    debug_edge_count_i <= debug_edge_count_i + 1;
                end if;

                if det_som /= (det_som'range => '0') then
                    debug_som_count_i <= debug_som_count_i + 1;
                end if;

                if msgs_valid /= (msgs_valid'range => '0') then
                    debug_msg_count_i <= debug_msg_count_i + to_unsigned(popcount(msgs_valid), debug_msg_count_i'length);
                    for i in 0 to NUM_DECODERS-1 loop
                        if msgs_valid(i) = '1' then
                            case message_df(msgs_decoded(i)) is
                                when 4 =>
                                    df4_hits := df4_hits + 1;
                                when 5 =>
                                    df5_hits := df5_hits + 1;
                                when 11 =>
                                    df11_hits := df11_hits + 1;
                                when 17 =>
                                    df17_hits := df17_hits + 1;
                                when 18 =>
                                    df18_hits := df18_hits + 1;
                                when others =>
                                    null;
                            end case;
                        end if;
                    end loop;
                    if df4_hits > 0 then
                        debug_df4_count_i <= debug_df4_count_i + to_unsigned(df4_hits, debug_df4_count_i'length);
                    end if;
                    if df5_hits > 0 then
                        debug_df5_count_i <= debug_df5_count_i + to_unsigned(df5_hits, debug_df5_count_i'length);
                    end if;
                    if df11_hits > 0 then
                        debug_df11_count_i <= debug_df11_count_i + to_unsigned(df11_hits, debug_df11_count_i'length);
                    end if;
                    if df17_hits > 0 then
                        debug_df17_count_i <= debug_df17_count_i + to_unsigned(df17_hits, debug_df17_count_i'length);
                    end if;
                    if df18_hits > 0 then
                        debug_df18_count_i <= debug_df18_count_i + to_unsigned(df18_hits, debug_df18_count_i'length);
                    end if;
                end if;
                if ENABLE_DEEP_DEBUG and decoder_smallest_done /= (decoder_smallest_done'range => '0') then
                    debug_smallest_done_count_i <= debug_smallest_done_count_i + to_unsigned(popcount(decoder_smallest_done), debug_smallest_done_count_i'length);
                end if;
                if decoder_invalid_df /= (decoder_invalid_df'range => '0') then
                    debug_invalid_df_count_i <= debug_invalid_df_count_i + to_unsigned(popcount(decoder_invalid_df), debug_invalid_df_count_i'length);
                end if;
                if ENABLE_DEEP_DEBUG and decoder_crc_attempt /= (decoder_crc_attempt'range => '0') then
                    debug_crc_attempt_count_i <= debug_crc_attempt_count_i + to_unsigned(popcount(decoder_crc_attempt), debug_crc_attempt_count_i'length);
                end if;
                if decoder_crc_pass /= (decoder_crc_pass'range => '0') then
                    debug_crc_pass_count_i <= debug_crc_pass_count_i + to_unsigned(popcount(decoder_crc_pass), debug_crc_pass_count_i'length);
                end if;
                if ENABLE_DEEP_DEBUG and decoder_crc_exhausted /= (decoder_crc_exhausted'range => '0') then
                    debug_crc_exhausted_count_i <= debug_crc_exhausted_count_i + to_unsigned(popcount(decoder_crc_exhausted), debug_crc_exhausted_count_i'length);
                end if;
                if ENABLE_DEEP_DEBUG and decoder_candidate_df4 /= (decoder_candidate_df4'range => '0') then
                    debug_cand_df4_count_i <= debug_cand_df4_count_i + to_unsigned(popcount(decoder_candidate_df4), debug_cand_df4_count_i'length);
                end if;
                if ENABLE_DEEP_DEBUG and decoder_candidate_df5 /= (decoder_candidate_df5'range => '0') then
                    debug_cand_df5_count_i <= debug_cand_df5_count_i + to_unsigned(popcount(decoder_candidate_df5), debug_cand_df5_count_i'length);
                end if;
                if ENABLE_DEEP_DEBUG and decoder_candidate_df11 /= (decoder_candidate_df11'range => '0') then
                    debug_cand_df11_count_i <= debug_cand_df11_count_i + to_unsigned(popcount(decoder_candidate_df11), debug_cand_df11_count_i'length);
                end if;
                if ENABLE_DEEP_DEBUG then
                    busy_now := popcount(decoder_busy);
                    if busy_now > to_integer(debug_decoder_busy_max_i) then
                        debug_decoder_busy_max_i <= to_unsigned(busy_now, debug_decoder_busy_max_i'length);
                    end if;
                    for i in 0 to NUM_DECODERS-1 loop
                        if decoder_crc0_valid(i) = '1' then
                            debug_crc0_w0_i <= decoder_crc0_w0(i);
                            debug_crc0_w1_i <= decoder_crc0_w1(i);
                            debug_crc0_w2_i <= decoder_crc0_w2(i);
                            debug_crc0_w3_i <= decoder_crc0_w3(i);
                        end if;
                    end loop;
                end if;
            end if;
        end if;
    end process;

    -- Debug output: current RPL from preamble detector
    debug_rpl <= det_rpl ;
    debug_edge_count <= debug_edge_count_i;
    debug_som_count <= debug_som_count_i;
    debug_msg_count <= debug_msg_count_i;
    debug_edge_shape_count <= debug_edge_shape_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_edge_qual_count <= debug_edge_qual_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_pass_count <= debug_preamble_pass_count_i;
    debug_preamble_detect_count <= debug_preamble_detect_count_i;
    debug_preamble_abs_count <= debug_preamble_abs_count_i;
    debug_preamble_quiet_count <= debug_preamble_quiet_count_i;
    debug_preamble_quiet_a_fail_count <= debug_preamble_quiet_a_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_quiet_b_fail_count <= debug_preamble_quiet_b_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_quiet_c_fail_count <= debug_preamble_quiet_c_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_quiet_d_fail_count <= debug_preamble_quiet_d_fail_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_snr_count <= debug_preamble_snr_count_i;
    debug_preamble_holdoff_count <= debug_preamble_holdoff_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_peak_age <= debug_preamble_peak_age_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_no_free_count <= debug_preamble_no_free_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_preamble_busy_drop_count <= debug_preamble_busy_drop_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_decoder_busy_max <= debug_decoder_busy_max_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_smallest_done_count <= debug_smallest_done_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_invalid_df_count <= debug_invalid_df_count_i;
    debug_crc_attempt_count <= debug_crc_attempt_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_crc_pass_count <= debug_crc_pass_count_i;
    debug_crc_exhausted_count <= debug_crc_exhausted_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_df4_count <= debug_df4_count_i;
    debug_df5_count <= debug_df5_count_i;
    debug_df11_count <= debug_df11_count_i;
    debug_cand_df4_count <= debug_cand_df4_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_cand_df5_count <= debug_cand_df5_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_cand_df11_count <= debug_cand_df11_count_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_df17_count <= debug_df17_count_i;
    debug_df18_count <= debug_df18_count_i;
    debug_crc0_w0 <= debug_crc0_w0_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_crc0_w1 <= debug_crc0_w1_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_crc0_w2 <= debug_crc0_w2_i when ENABLE_DEEP_DEBUG else (others => '0');
    debug_crc0_w3 <= debug_crc0_w3_i when ENABLE_DEEP_DEBUG else (others => '0');

    -- Route decoded messages and timestamps to top-level outputs
    out_messages <= msgs_decoded ;
    out_valid <= msgs_valid ;
    out_toas <= decoder_toas ;
    out_rpls <= decoder_rpls ;

end architecture ;
