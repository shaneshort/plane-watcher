-- =============================================================================
-- log_to_linear_tb.vhd -- Testbench for antilog LUT
-- =============================================================================
--
-- Verifies:
--   - Single-cycle latency from in_valid to out_valid
--   - Monotonic output (placeholder table is monotonic; higher code = higher
--     linear power under the default non-inverting polarity assumption)
--   - Endpoint values are sane: code 0 maps to a small number (noise floor),
--     full-scale code maps to a large number
--   - in_valid controls out_valid propagation
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;
    use work.logdet_pkg.all;

entity log_to_linear_tb is
end entity;

architecture tb of log_to_linear_tb is
    constant ADC_WIDTH    : positive := LOGDET_ADC_WIDTH;
    constant OUTPUT_WIDTH : positive := INPUT_POWER_WIDTH;
    -- AD9203 target rate is 40 MSPS.
    constant CLK_PERIOD   : time := 25 ns;

    signal clock : std_logic := '0';
    signal reset : std_logic := '1';

    signal in_code  : unsigned(ADC_WIDTH-1 downto 0) := (others => '0');
    signal in_valid : std_logic := '0';

    signal out_power : signed(OUTPUT_WIDTH-1 downto 0);
    signal out_valid : std_logic;

    signal sim_done : boolean := false;
begin

    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.log_to_linear
        generic map (
            ADC_WIDTH    => ADC_WIDTH,
            OUTPUT_WIDTH => OUTPUT_WIDTH
        )
        port map (
            clock     => clock,
            reset     => reset,
            in_code   => in_code,
            in_valid  => in_valid,
            out_power => out_power,
            out_valid => out_valid
        );

    stim: process
        procedure wait_clocks(n : positive) is
        begin
            for i in 1 to n loop
                wait until rising_edge(clock);
            end loop;
            -- Step past the rising edge so any registers clocked on this
            -- edge have their outputs visible before downstream assertions.
            wait for 1 ps;
        end procedure;

        variable prev_power : signed(OUTPUT_WIDTH-1 downto 0);
        variable cur_power  : signed(OUTPUT_WIDTH-1 downto 0);
        variable low_power  : signed(OUTPUT_WIDTH-1 downto 0);
        variable high_power : signed(OUTPUT_WIDTH-1 downto 0);
    begin
        reset <= '1';
        wait_clocks(5);
        reset <= '0';
        wait_clocks(2);

        -- Test 1: single-cycle latency.
        report "Test 1: LUT latency";
        in_code  <= to_unsigned(512, ADC_WIDTH);
        in_valid <= '1';
        wait_clocks(1);
        -- After one rising edge, out_valid should be high.
        assert out_valid = '1'
            report "out_valid should pulse one cycle after in_valid"
            severity failure;
        in_valid <= '0';
        wait_clocks(1);
        assert out_valid = '0'
            report "out_valid should drop when in_valid drops (plus 1 cycle delay)"
            severity failure;
        report "Test 1: PASSED";

        -- Test 2: Endpoint values — code 0 gives lowest output, full-scale
        -- gives highest (placeholder table is monotonically increasing).
        report "Test 2: endpoint values";
        in_code  <= (others => '0');
        in_valid <= '1';
        wait_clocks(1);
        wait_clocks(1);
        low_power := out_power;
        report "LUT[0]    = " & integer'image(to_integer(low_power));

        in_code <= (others => '1');
        wait_clocks(1);
        wait_clocks(1);
        high_power := out_power;
        report "LUT[full-scale] = " & integer'image(to_integer(high_power));

        assert low_power >= to_signed(0, OUTPUT_WIDTH)
            report "LUT output must be non-negative"
            severity failure;
        assert high_power > low_power
            report "LUT[full-scale] must exceed LUT[zero]"
            severity failure;
        in_valid <= '0';
        report "Test 2: PASSED";

        -- Test 3: Monotonicity across a coarse sweep.
        -- Placeholder antilog table is strictly increasing in input code
        -- (higher code = higher dBm = higher linear power).
        report "Test 3: monotonic sweep";
        in_valid <= '1';
        in_code  <= to_unsigned(0, ADC_WIDTH);
        wait_clocks(1);
        wait_clocks(1);  -- align to first sampled output
        prev_power := out_power;

        for i in 1 to 15 loop
            -- Step through 16 points across the 1024-entry table.
            in_code <= to_unsigned(i * 64, ADC_WIDTH);
            wait_clocks(1);
            wait_clocks(1);
            cur_power := out_power;
            assert cur_power >= prev_power
                report "non-monotonic at step " & integer'image(i) &
                       ": prev=" & integer'image(to_integer(prev_power)) &
                       " cur=" & integer'image(to_integer(cur_power))
                severity failure;
            prev_power := cur_power;
        end loop;
        in_valid <= '0';
        report "Test 3: PASSED";

        -- Test 4: Mid-range "typical pulse" lands in a useful range.
        -- REF_DBM is -50 dBm in the placeholder table, near ADC code 448,
        -- mapping to ~8000. Sanity-check that this code gives a non-trivial
        -- power.
        report "Test 4: midpoint sanity";
        in_code  <= to_unsigned(448, ADC_WIDTH);
        in_valid <= '1';
        wait_clocks(1);
        wait_clocks(1);
        report "LUT[448] = " & integer'image(to_integer(out_power));
        -- Loose bounds - exact value depends on placeholder curve, but it
        -- must be clearly above the noise floor and below full-scale.
        assert out_power > to_signed(100, OUTPUT_WIDTH)
            report "midpoint output too small - table scaling off"
            severity failure;
        assert out_power < to_signed(2**(OUTPUT_WIDTH-1) - 1, OUTPUT_WIDTH)
            report "midpoint output clipped - table scaling off"
            severity failure;
        in_valid <= '0';
        report "Test 4: PASSED";

        report "== All log_to_linear tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;

end architecture;
