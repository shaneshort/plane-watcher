library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity timestamp_counter_tb is
end entity;

architecture tb of timestamp_counter_tb is
    constant CLK_PERIOD : time := 62.5 ns;  -- 16 MHz
    signal clock          : std_logic := '0';
    signal reset          : std_logic := '1';
    signal pps_in         : std_logic := '0';
    signal counter_value  : unsigned(COUNTER_WIDTH-1 downto 0);
    signal counter_at_pps : unsigned(COUNTER_WIDTH-1 downto 0);
    signal pps_count      : unsigned(31 downto 0);
    signal pps_new        : std_logic;
    signal sim_done : boolean := false;
begin
    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.timestamp_counter
        generic map (WIDTH => COUNTER_WIDTH)
        port map (
            clock          => clock,
            reset          => reset,
            pps_in         => pps_in,
            counter_value  => counter_value,
            counter_at_pps => counter_at_pps,
            pps_count      => pps_count,
            pps_new        => pps_new
        );

    stim: process
        procedure wait_clocks(n : positive) is
        begin
            for i in 1 to n loop
                wait until rising_edge(clock);
            end loop;
        end procedure;
    begin
        reset <= '1';
        wait_clocks(10);
        reset <= '0';
        wait_clocks(5);

        -- Test 1: Counter incrementing
        report "Test 1: Counter incrementing";
        assert counter_value > to_unsigned(0, COUNTER_WIDTH)
            report "Counter should be non-zero after reset released"
            severity failure;
        wait_clocks(10);
        assert counter_value >= to_unsigned(14, COUNTER_WIDTH)
            report "Counter should have advanced to at least 14"
            severity failure;
        report "Test 1: PASSED";

        -- Test 2: PPS capture
        report "Test 2: PPS capture";
        assert pps_count = to_unsigned(0, 32)
            report "PPS count should be 0 before any PPS"
            severity failure;
        wait_clocks(100);
        pps_in <= '1';
        wait_clocks(10);
        pps_in <= '0';
        wait_clocks(5);
        assert pps_count = to_unsigned(1, 32)
            report "PPS count should be 1 after first PPS, got " &
                   integer'image(to_integer(pps_count))
            severity failure;
        assert counter_at_pps > to_unsigned(0, COUNTER_WIDTH)
            report "counter_at_pps should be non-zero after PPS"
            severity failure;
        report "Test 2: PASSED -- PPS latched at counter = " &
               integer'image(to_integer(counter_at_pps(31 downto 0)));

        -- Test 3: Second PPS
        report "Test 3: Second PPS event";
        wait_clocks(1000);
        pps_in <= '1';
        wait_clocks(10);
        pps_in <= '0';
        wait_clocks(5);
        assert pps_count = to_unsigned(2, 32)
            report "PPS count should be 2 after second PPS"
            severity failure;
        report "Test 3: PASSED -- Second PPS latched at counter = " &
               integer'image(to_integer(counter_at_pps(31 downto 0)));

        -- Test 4: pps_new single-cycle pulse
        report "Test 4: pps_new pulse width";
        wait_clocks(500);
        pps_in <= '1';
        -- Wait for synchroniser propagation
        wait_clocks(4);
        -- pps_new should now be high
        assert pps_new = '1'
            report "pps_new should have pulsed"
            severity failure;
        -- After one more clock it should be low
        wait_clocks(1);
        assert pps_new = '0'
            report "pps_new should be single-cycle pulse"
            severity failure;
        pps_in <= '0';
        report "Test 4: PASSED";

        report "== All timestamp_counter tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;
end architecture;
