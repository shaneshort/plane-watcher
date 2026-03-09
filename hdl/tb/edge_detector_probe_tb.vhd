-- Quick probe: feed known power values into edge detector, report edges
library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;
library work;
    use work.adsb_pkg.all;

entity edge_detector_probe_tb is
end entity;

architecture tb of edge_detector_probe_tb is
    constant CLK_PERIOD : time := 62.5 ns;
    signal clock     : std_logic := '0';
    signal reset     : std_logic := '1';
    signal power_in  : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal in_valid  : std_logic := '0';
    signal power_out : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal edge_out  : std_logic;
    signal out_valid : std_logic;
    signal sim_done  : boolean := false;
begin
    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.adsb_edge_detector
        port map (
            clock => clock, reset => reset, init => '0',
            power_in => power_in, in_valid => in_valid,
            power_out => power_out, edge_out => edge_out, out_valid => out_valid
        );

    -- Monitor edges
    mon: process(clock)
        variable edge_count : integer := 0;
        variable cycle : integer := 0;
    begin
        if rising_edge(clock) then
            cycle := cycle + 1;
            if out_valid = '1' and edge_out = '1' then
                edge_count := edge_count + 1;
                report "EDGE detected at cycle " & integer'image(cycle) &
                       " power_out=" & integer'image(to_integer(power_out)) &
                       " (edge #" & integer'image(edge_count) & ")";
            end if;
        end if;
    end process;

    stim: process
        procedure wait_clocks(n : positive) is
        begin
            for i in 1 to n loop wait until rising_edge(clock); end loop;
        end procedure;

        procedure assert_quiescent(tag : string) is
        begin
            assert out_valid = '0'
                report tag & ": expected out_valid=0 after reset"
                severity failure;
            assert edge_out = '0'
                report tag & ": expected edge_out=0 after reset"
                severity failure;
            assert power_out = to_signed(0, INPUT_POWER_WIDTH)
                report tag & ": expected power_out=0 after reset"
                severity failure;
        end procedure;

        -- Feed a single Mode-S preamble pulse pattern with pulse-shaped edges
        -- Pulse pattern: 8 on, 8 off, 8 on, 8 off (×4 gap), 8 on, 8 off, 8 on, 8 off, rest off
        -- With 1-sample ramps at transitions
        constant PWR_HI  : signed(INPUT_POWER_WIDTH-1 downto 0) := to_signed(2097152, INPUT_POWER_WIDTH);
        constant PWR_MID : signed(INPUT_POWER_WIDTH-1 downto 0) := to_signed(524288, INPUT_POWER_WIDTH);
        constant PWR_LO  : signed(INPUT_POWER_WIDTH-1 downto 0) := to_signed(0, INPUT_POWER_WIDTH);

        -- Emit one pulse: ramp up, hold, ramp down
        procedure emit_pulse(width : positive) is
        begin
            -- Ramp up
            power_in <= PWR_MID; in_valid <= '1'; wait_clocks(1);
            -- Hold high
            for i in 1 to width-2 loop
                power_in <= PWR_HI; in_valid <= '1'; wait_clocks(1);
            end loop;
            -- Ramp down
            power_in <= PWR_MID; in_valid <= '1'; wait_clocks(1);
        end procedure;

        procedure emit_gap(width : positive) is
        begin
            for i in 1 to width loop
                power_in <= PWR_LO; in_valid <= '1'; wait_clocks(1);
            end loop;
        end procedure;

    begin
        reset <= '1';
        wait_clocks(20);
        reset <= '0';
        wait_clocks(5);
        assert_quiescent("initial reset release");

        -- Exercise a mid-stream reset so stale history would be visible if the
        -- detector state were not explicitly cleared.
        emit_pulse(8);
        emit_gap(8);
        in_valid <= '0';
        wait_clocks(2);
        reset <= '1';
        wait_clocks(2);
        reset <= '0';
        wait_clocks(3);
        assert_quiescent("mid-stream reset release");

        report "Feeding preamble-like pattern...";
        in_valid <= '1';

        -- Lead-in silence
        emit_gap(50);

        -- Preamble: pulses at 0, 1, 3.5, 4.5 µs (in chips: 0, 2, 7, 9)
        -- At SPS=8: positions 0, 16, 56, 72
        emit_pulse(8);   -- Pulse 0 (samples 0-7)
        emit_gap(8);     -- Gap (samples 8-15)
        emit_pulse(8);   -- Pulse 1 (samples 16-23)
        emit_gap(32);    -- Gap (samples 24-55, 4 chips)
        emit_pulse(8);   -- Pulse 2 (samples 56-63)
        emit_gap(8);     -- Gap (samples 64-71)
        emit_pulse(8);   -- Pulse 3 (samples 72-79)
        emit_gap(48);    -- Rest of preamble + some data

        -- Trailing silence
        emit_gap(100);

        in_valid <= '0';
        wait_clocks(50);

        report "== Edge detector probe complete ==";
        sim_done <= true;
        wait;
    end process;
end architecture;
