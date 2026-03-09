-- =============================================================================
-- bsd_calculator.vhd — Bit Soft-Decision Calculator for Mode-S Demodulation
-- =============================================================================
--
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
--
-- Mode-S uses Pulse Position Modulation (PPM): each data bit occupies two
-- "chips" (half-bit periods). A '1' bit is [high, low] and a '0' bit is
-- [low, high]. At SPS=8 samples per chip, each bit spans 16 samples:
--   - Samples  0–7  = first chip  (chip A)
--   - Samples  8–15 = second chip (chip B)
--
-- For a '1' bit: chip A has signal, chip B is quiet → score1 > score0
-- For a '0' bit: chip A is quiet, chip B has signal → score0 > score1
--
-- This module computes a "soft decision" for each bit — not just 1/0, but
-- a confidence score indicating how strongly the sample pattern favours
-- one value over the other. Bits with low confidence (small |score1 - score0|)
-- are candidates for error correction by the downstream bit_flipper.
--
-- Algorithm:
--   1. Classify each sample as typeA (near the Reference Power Level) or
--      typeB (well below RPL) based on amplitude thresholds derived from RPL:
--        - typeA: sample is within [RPL - 0.75×RPL, RPL + 0.75×RPL]
--        - typeB: sample is below (RPL - 0.75×RPL) / 2
--        - Neither: sample doesn't clearly belong to either class
--
--   2. Apply per-sample weights: [0,0,1,1,1,1,0,0] across each chip.
--      The edge samples (0,1,6,7) get weight 0 because transitions between
--      chips cause unreliable values there. Only the centre samples (2–5)
--      contribute to the score.
--
--   3. For each bit, compute:
--        score1 = Σ (typeA[chip_A] - typeA[chip_B] - typeB[chip_A] + typeB[chip_B]) × weight
--        score0 = Σ (typeA[chip_B] - typeA[chip_A] - typeB[chip_B] + typeB[chip_A]) × weight
--      (score0 and score1 are complements — they have opposite signs)
--
--   4. Output the Bit Soft-Decision: BSD = score1 - score0 (signed 8-bit)
--      and the Bit Hard Decision: BHD = '1' if score1 > score0, else '0'
--
-- The BSD magnitude indicates confidence. A large |BSD| means the bit is
-- clearly one value; a small |BSD| means the bit is ambiguous and could
-- have been corrupted by noise or interference.
-- =============================================================================

library ieee;
    use ieee.numeric_std.all;
    use ieee.std_logic_1164.all;

library work ;
    use work.adsb_pkg.all ;

entity bsd_calculator is
  port(
    clock           : in std_logic;                                       -- System clock
    reset           : in std_logic;                                       -- Asynchronous reset

    rpl_in          : in signed(INPUT_POWER_WIDTH-1 downto 0);            -- Reference Power Level from preamble detector
    rpl_valid       : in std_logic;                                       -- RPL strobe (once per message start)

    power_in        : in signed(INPUT_POWER_WIDTH-1 downto 0);            -- Input power sample
    power_in_valid  : in std_logic;                                       -- Input valid strobe

    bsd             : out signed(7 downto 0);                             -- Bit Soft-Decision (confidence score)
    bhd             : out std_logic;                                      -- Bit Hard Decision (1 or 0)
    out_valid       : out std_logic                                       -- Output valid strobe (one per bit)
  );
end entity;

architecture arch of bsd_calculator is

    -- Per-sample weights within each chip. The edge samples (0,1 and 6,7)
    -- get weight 0 because chip transitions corrupt these samples.
    -- Only the 4 centre samples (indices 2–5) contribute to the score.
    type weight_array_t is array(natural range <>) of integer range 0 to 3;
    constant weights    : weight_array_t(0 to SPS-1) := (0,0,1,1,1,1,0,0) ;

    -- Sample counter within each 16-sample bit period (0 to 2×SPS-1 = 15)
    signal sample_count : integer range 0 to 2*SPS;

    -- Running scores for the current bit. score1 accumulates evidence for '1',
    -- score0 accumulates evidence for '0'. They are complementary.
    signal score0       : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal score1       : signed(INPUT_POWER_WIDTH-1 downto 0);

    -- Registered copies of the final scores (captured when a bit completes)
    signal score0_reg   : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal score1_reg   : signed(INPUT_POWER_WIDTH-1 downto 0);

    -- RPL-derived thresholds for sample classification.
    -- rpl_low  = RPL - 0.75 × RPL = 0.25 × RPL  (lower bound for typeA)
    -- rpl_high = RPL + 0.75 × RPL = 1.75 × RPL  (upper bound for typeA)
    -- rpl_lowlow = rpl_low / 2                    (threshold for typeB)
    --
    -- The 0.75 factor is computed as (shift_right(1) + shift_right(2)) = 0.5 + 0.25 = 0.75,
    -- using only shifts and adds (no multiplier needed).
    signal rpl_low      : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal rpl_high     : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal rpl_lowlow   : signed(INPUT_POWER_WIDTH-1 downto 0);

    -- Sample classification arrays for the current bit (16 samples total).
    -- typeA(i) = 1 if sample i is near RPL (signal present), 0 otherwise.
    -- typeB(i) = 1 if sample i is well below RPL (noise/quiet), 0 otherwise.
    type bsd_hit_t is array(natural range <>) of signed(7 downto 0);
    signal typeA        : bsd_hit_t(0 to 2*SPS-1);
    signal typeB        : bsd_hit_t(0 to 2*SPS-1);

    -- Pipeline control: bsd_request is high for one cycle when a bit's
    -- scores are ready; score_it is the delayed valid for score computation.
    signal bsd_request  : std_logic;
    signal score_it     : std_logic;
    signal bit_debug_idx : integer range 0 to 255;

begin

    -- =========================================================================
    -- Main scoring process
    --
    -- This process has three interleaved pipelines operating on each input sample:
    --
    -- Stage 1 (power_in_valid): Classify the sample as typeA/typeB and store
    --   in the classification arrays. Also set RPL thresholds when rpl_valid fires.
    --
    -- Stage 2 (score_it = delayed power_in_valid): Accumulate the weighted
    --   cross-correlation scores for the current bit. When all 16 samples
    --   of a bit have been processed, register the final scores.
    --
    -- Stage 3 (bsd_request): Compute the BSD (score1 - score0) and BHD,
    --   output them with out_valid.
    -- =========================================================================
    calculate : process(clock,reset)
        variable sps_downcount : integer range 0 to SPS;
        variable b0_index : integer range 0 to SPS-1;          -- Index within chip A (0–7)
        variable b1_index : integer range SPS to (2*SPS)-1;    -- Index within chip B (8–15)
        variable local_count : integer range 0 to 15;          -- Sample counter within scoring

        variable tmp_score1 : signed(INPUT_POWER_WIDTH-1 downto 0);
        variable tmp_score0 : signed(INPUT_POWER_WIDTH-1 downto 0);
        variable next_score1 : signed(INPUT_POWER_WIDTH-1 downto 0);
        variable next_score0 : signed(INPUT_POWER_WIDTH-1 downto 0);
    begin
        if(reset ='1') then
            score0 <= (others => '0');
            score1 <= (others => '0');
            score0_reg <= (others => '0');
            score1_reg <= (others => '0');
            bsd_request <= '0';
            score_it <= '0';
            sample_count <= 0;
            out_valid <= '0';
            bsd <= (others => '0');
            bhd <= '0';
            rpl_low <= (others => '0');
            rpl_high <= (others => '0');
            rpl_lowlow <= (others => '0');
            bit_debug_idx <= 0;

            b0_index := 0;
            b1_index := SPS;

        elsif( rising_edge(clock)) then
            bsd_request <= '0';

            -- Output pipeline: one cycle after bsd_request, emit the final BSD
            out_valid <= bsd_request;
            if(bsd_request ='1') then
                -- BSD = score1 - score0, truncated to 8 bits.
                -- Positive BSD → likely '1' bit. Negative → likely '0'.
                -- Magnitude = confidence.
                bsd <= resize(score1_reg - score0_reg, bsd'length);
                if(score1_reg > score0_reg) then
                    bhd <= '1';     -- Hard decision: '1'
                    -- synthesis translate_off
                    if bit_debug_idx < 24 then
                        report "BSD_DBG: bit=" & integer'image(bit_debug_idx) &
                               " score1=" & integer'image(to_integer(score1_reg)) &
                               " score0=" & integer'image(to_integer(score0_reg)) &
                               " bsd=" & integer'image(to_integer(resize(score1_reg - score0_reg, bsd'length))) &
                               " bhd='1'";
                    end if;
                    -- synthesis translate_on
                else
                    bhd <= '0';     -- Hard decision: '0'
                    -- synthesis translate_off
                    if bit_debug_idx < 24 then
                        report "BSD_DBG: bit=" & integer'image(bit_debug_idx) &
                               " score1=" & integer'image(to_integer(score1_reg)) &
                               " score0=" & integer'image(to_integer(score0_reg)) &
                               " bsd=" & integer'image(to_integer(resize(score1_reg - score0_reg, bsd'length))) &
                               " bhd='0'";
                    end if;
                    -- synthesis translate_on
                end if;
                bit_debug_idx <= bit_debug_idx + 1;
            end if;

            -- Score accumulation (one cycle after sample classification)
            if score_it = '1' then
                -- Only start scoring after 8 samples (the first chip must be
                -- fully in the classification array before cross-chip comparison)
                if(local_count > 7) then
                    -- Compute this sample's contribution to score1:
                    -- Positive: typeA in chip A (signal where expected for '1')
                    --           typeB in chip B (quiet where expected for '1')
                    -- Negative: typeA in chip B (signal where NOT expected for '1')
                    --           typeB in chip A (quiet where NOT expected for '1')
                    -- All scaled by the per-sample weight.
                    tmp_score1 := resize(shift_left(typeA(b0_index), weights(b0_index)) -
                                        shift_left(typeA(b1_index), weights(b0_index)) -
                                        shift_left(typeB(b0_index),weights(b0_index)) +
                                        shift_left(typeB(b1_index),weights(b0_index)),tmp_score1'length);

                    next_score1 := score1 + tmp_score1;

                    -- score0 is the complement (swap chip A and chip B roles)
                    tmp_score0 := resize(shift_left(typeA(b1_index),weights(b0_index)) -
                                        shift_left(typeA(b0_index),weights(b0_index)) -
                                        shift_left(typeB(b1_index),weights(b0_index)) +
                                        shift_left(typeB(b0_index),weights(b0_index)),tmp_score0'length);

                    next_score0 := score0 + tmp_score0;

                    -- After processing all 8 weighted sample pairs, the bit is done
                    if(b0_index = 7) then
                        bsd_request <= '1';           -- Trigger output next cycle
                        -- Capture the full score including the final chip pair.
                        score1_reg <= next_score1;
                        score0_reg <= next_score0;
                        score1 <= (others => '0');     -- Reset accumulators for next bit
                        score0 <= (others => '0');
                        b0_index := 0;
                        b1_index := SPS;
                    else
                        score1 <= next_score1;
                        score0 <= next_score0;
                        b0_index := b0_index + 1;
                        b1_index := b1_index + 1;
                    end if;
                end if;

                -- Track position within the 16-sample bit period
                if (local_count = 15) then
                    local_count := 0;
                else
                    local_count := local_count + 1;
                end if;
            end if;

            -- Arm scoring for next cycle
            score_it <= power_in_valid;

            -- Sample classification: determine if each power sample is
            -- "near RPL" (typeA), "well below RPL" (typeB), or neither.
            if(power_in_valid = '1') then
                if ((power_in > rpl_low) and (power_in < rpl_high)) then
                    -- TypeA: sample is within ±75% of RPL → signal present
                    typeA(sample_count) <= to_signed(1,8);
                    typeB(sample_count) <=to_signed(0,8);
                elsif (power_in < rpl_lowlow) then
                    -- TypeB: sample is below half of rpl_low → noise/quiet
                    typeA(sample_count) <= to_signed(0,8);
                    typeB(sample_count) <= to_signed(1,8);
                else
                    -- Ambiguous: sample doesn't clearly belong to either class
                    typeA(sample_count) <= to_signed(0,8);
                    typeB(sample_count) <= to_signed(0,8);
                end if;

                -- Advance sample counter within the bit period (wraps at 15)
                if(sample_count = 15) then
                    sample_count <= 0;
                else
                    sample_count <= sample_count + 1;
                end if;
            end if;

            -- On RPL valid: compute classification thresholds from RPL.
            -- Uses only shifts and adds (no multiplier). Constants from adsb_pkg.
            -- Also reset the sample counter for the new message.
            if(rpl_valid = '1') then
                rpl_low <= rpl_in - shift_right(rpl_in, BSD_RPL_LOW_SUB_SHIFT);
                rpl_high <= rpl_in + shift_right(rpl_in, BSD_RPL_HIGH_ADD1_SHIFT)
                                   + shift_right(rpl_in, BSD_RPL_HIGH_ADD2_SHIFT);
                rpl_lowlow <= shift_right(
                    rpl_in - shift_right(rpl_in, BSD_RPL_LOW_SUB_SHIFT),
                    BSD_RPL_LOWLOW_SHIFT);
                sample_count <= 0;
                local_count := 0;
                bit_debug_idx <= 0;
            end if;

        end if;
    end process;

end architecture;
