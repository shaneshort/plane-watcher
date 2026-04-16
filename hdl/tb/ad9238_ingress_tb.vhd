-- =============================================================================
-- ad9238_ingress_tb.vhd -- Testbench for AD9238 parallel CMOS ingress
-- =============================================================================
--
-- Verifies:
--   - Registered output follows the input one clock cycle later
--   - Both channels are captured independently
--   - OTR flags pass through correctly
--   - out_valid asserts after reset release and stays high
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity ad9238_ingress_tb is
end entity;

architecture tb of ad9238_ingress_tb is
    constant ADC_WIDTH : positive := 12;
    -- 65 MSPS → period 15.384 ns. Use 15.4 ns for readable sim time.
    constant CLK_PERIOD : time := 15.4 ns;

    signal clock : std_logic := '0';
    signal reset : std_logic := '1';

    signal adc_data_a : std_logic_vector(ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_otr_a  : std_logic := '0';
    signal adc_data_b : std_logic_vector(ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_otr_b  : std_logic := '0';

    signal out_data_a : unsigned(ADC_WIDTH-1 downto 0);
    signal out_otr_a  : std_logic;
    signal out_data_b : unsigned(ADC_WIDTH-1 downto 0);
    signal out_otr_b  : std_logic;
    signal out_valid  : std_logic;

    signal sim_done : boolean := false;
begin

    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.ad9238_ingress
        generic map (ADC_WIDTH => ADC_WIDTH)
        port map (
            clock      => clock,
            reset      => reset,
            adc_data_a => adc_data_a,
            adc_otr_a  => adc_otr_a,
            adc_data_b => adc_data_b,
            adc_otr_b  => adc_otr_b,
            out_data_a => out_data_a,
            out_otr_a  => out_otr_a,
            out_data_b => out_data_b,
            out_otr_b  => out_otr_b,
            out_valid  => out_valid
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

        -- Test 2: Channel A input propagates to output one cycle later.
        report "Test 2: channel A latch timing";
        adc_data_a <= std_logic_vector(to_unsigned(16#A5A#, ADC_WIDTH));
        wait_clocks(1);
        -- After one rising edge the value should appear at the output.
        assert out_data_a = to_unsigned(16#A5A#, ADC_WIDTH)
            report "channel A output did not match input after 1 cycle: got " &
                   integer'image(to_integer(out_data_a))
            severity failure;
        report "Test 2: PASSED";

        -- Test 3: Channel B is independent of channel A.
        report "Test 3: channel B independence";
        adc_data_a <= std_logic_vector(to_unsigned(16#123#, ADC_WIDTH));
        adc_data_b <= std_logic_vector(to_unsigned(16#ABC#, ADC_WIDTH));
        wait_clocks(1);
        assert out_data_a = to_unsigned(16#123#, ADC_WIDTH)
            report "channel A did not update"
            severity failure;
        assert out_data_b = to_unsigned(16#ABC#, ADC_WIDTH)
            report "channel B did not match: got " &
                   integer'image(to_integer(out_data_b))
            severity failure;
        report "Test 3: PASSED";

        -- Test 4: OTR flag passthrough per channel.
        report "Test 4: OTR flags";
        adc_otr_a <= '1';
        adc_otr_b <= '0';
        wait_clocks(1);
        assert out_otr_a = '1' and out_otr_b = '0'
            report "OTR A should be 1, OTR B should be 0"
            severity failure;
        adc_otr_a <= '0';
        adc_otr_b <= '1';
        wait_clocks(1);
        assert out_otr_a = '0' and out_otr_b = '1'
            report "OTR A should be 0, OTR B should be 1"
            severity failure;
        report "Test 4: PASSED";

        -- Test 5: Full-scale and zero-scale codes.
        report "Test 5: endpoint codes";
        adc_data_a <= (others => '0');
        adc_data_b <= (others => '1');
        wait_clocks(1);
        assert out_data_a = to_unsigned(0, ADC_WIDTH)
            report "channel A should be 0"
            severity failure;
        assert out_data_b = to_unsigned(2**ADC_WIDTH - 1, ADC_WIDTH)
            report "channel B should be full-scale"
            severity failure;
        report "Test 5: PASSED";

        report "== All ad9238_ingress tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;

end architecture;
