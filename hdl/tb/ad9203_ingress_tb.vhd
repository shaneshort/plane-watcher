-- =============================================================================
-- ad9203_ingress_tb.vhd -- Testbench for AD9203 parallel CMOS ingress
-- =============================================================================
--
-- Verifies:
--   - Registered output follows the input one clock cycle later
--   - OTR flag passes through correctly
--   - out_valid asserts after reset release and stays high
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity ad9203_ingress_tb is
end entity;

architecture tb of ad9203_ingress_tb is
    constant ADC_WIDTH : positive := 10;
    -- 40 MSPS -> period 25 ns.
    constant CLK_PERIOD : time := 25 ns;

    signal clock : std_logic := '0';
    signal reset : std_logic := '1';

    signal adc_data : std_logic_vector(ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_otr  : std_logic := '0';

    signal out_data : unsigned(ADC_WIDTH-1 downto 0);
    signal out_otr  : std_logic;
    signal out_valid  : std_logic;

    signal sim_done : boolean := false;
begin

    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.ad9203_ingress
        generic map (ADC_WIDTH => ADC_WIDTH)
        port map (
            clock     => clock,
            reset     => reset,
            adc_data  => adc_data,
            adc_otr   => adc_otr,
            out_data  => out_data,
            out_otr   => out_otr,
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
    begin
        reset <= '1';
        wait_clocks(5);
        reset <= '0';

        -- Test 1: valid deasserted under reset, asserted after release
        report "Test 1: out_valid tracks reset";
        assert out_valid = '0'
            report "out_valid should be 0 under reset"
            severity failure;
        wait_clocks(2);
        assert out_valid = '1'
            report "out_valid should be 1 after reset release"
            severity failure;
        report "Test 1: PASSED";

        -- Test 2: ADC input propagates to output one cycle later.
        report "Test 2: ADC latch timing";
        adc_data <= std_logic_vector(to_unsigned(16#25A#, ADC_WIDTH));
        wait_clocks(1);
        -- After one rising edge the value should appear at the output.
        assert out_data = to_unsigned(16#25A#, ADC_WIDTH)
            report "ADC output did not match input after 1 cycle: got " &
                   integer'image(to_integer(out_data))
            severity failure;
        report "Test 2: PASSED";

        -- Test 3: Input code updates on every clock.
        report "Test 3: continuous sample updates";
        adc_data <= std_logic_vector(to_unsigned(16#123#, ADC_WIDTH));
        wait_clocks(1);
        assert out_data = to_unsigned(16#123#, ADC_WIDTH)
            report "ADC output did not update"
            severity failure;
        report "Test 3: PASSED";

        -- Test 4: OTR flag passthrough.
        report "Test 4: OTR flag";
        adc_otr <= '1';
        wait_clocks(1);
        assert out_otr = '1'
            report "OTR should be 1"
            severity failure;
        adc_otr <= '0';
        wait_clocks(1);
        assert out_otr = '0'
            report "OTR should be 0"
            severity failure;
        report "Test 4: PASSED";

        -- Test 5: Full-scale and zero-scale codes.
        report "Test 5: endpoint codes";
        adc_data <= (others => '0');
        wait_clocks(1);
        assert out_data = to_unsigned(0, ADC_WIDTH)
            report "ADC output should be 0"
            severity failure;
        adc_data <= (others => '1');
        wait_clocks(1);
        assert out_data = to_unsigned(2**ADC_WIDTH - 1, ADC_WIDTH)
            report "ADC output should be full-scale"
            severity failure;
        report "Test 5: PASSED";

        report "== All ad9203_ingress tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;

end architecture;
