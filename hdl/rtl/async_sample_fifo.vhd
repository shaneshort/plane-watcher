-- =============================================================================
-- async_sample_fifo.vhd -- Small dual-clock FIFO for ingress sample crossing
-- =============================================================================
--
-- Crosses the 24-bit scalar sample power stream from the AD936x RX clock
-- domain into the 100 MHz decode clock domain. Unlike the earlier hold-data
-- + toggle scheme, this preserves ordering even when the downsampler emits
-- consecutive output samples on back-to-back 30.72 MHz input beats.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity async_sample_fifo is
    generic (
        WIDTH : positive := 24;
        DEPTH : positive := 64
    );
    port (
        -- Write side
        wr_clock : in  std_logic;
        wr_reset : in  std_logic;
        wr_data  : in  signed(WIDTH-1 downto 0);
        wr_en    : in  std_logic;
        full     : out std_logic;
        overflow : out std_logic;

        -- Read side
        rd_clock : in  std_logic;
        rd_reset : in  std_logic;
        rd_data  : out signed(WIDTH-1 downto 0);
        rd_en    : in  std_logic;
        empty    : out std_logic
    );
end entity;

architecture rtl of async_sample_fifo is
    function ceil_log2(v : positive) return natural is
        variable value  : natural := v - 1;
        variable result : natural := 0;
    begin
        while value > 0 loop
            value := value / 2;
            result := result + 1;
        end loop;
        return result;
    end function;

    function is_power_of_two(v : positive) return boolean is
        variable value : natural := v;
    begin
        while (value mod 2) = 0 loop
            value := value / 2;
        end loop;
        return value = 1;
    end function;

    function bin_to_gray(v : unsigned) return unsigned is
    begin
        return v xor shift_right(v, 1);
    end function;

    function gray_to_bin(v : unsigned) return unsigned is
        variable result : unsigned(v'range) := (others => '0');
    begin
        result(result'high) := v(v'high);
        for i in result'high - 1 downto 0 loop
            result(i) := result(i + 1) xor v(i);
        end loop;
        return result;
    end function;

    constant ADDR_BITS : positive := ceil_log2(DEPTH);
    constant PTR_BITS  : positive := ADDR_BITS + 1;

    type memory_t is array (0 to DEPTH - 1) of std_logic_vector(WIDTH-1 downto 0);
    signal mem : memory_t := (others => (others => '0'));
    attribute ram_style : string;
    attribute ram_style of mem : signal is "block";

    signal wr_ptr_bin  : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal wr_ptr_gray : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal rd_ptr_bin  : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal rd_ptr_gray : unsigned(PTR_BITS-1 downto 0) := (others => '0');

    signal rd_ptr_gray_sync1_w : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal rd_ptr_gray_sync2_w : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal wr_ptr_gray_sync1_r : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal wr_ptr_gray_sync2_r : unsigned(PTR_BITS-1 downto 0) := (others => '0');

    signal full_i       : std_logic := '0';
    signal empty_i      : std_logic := '1';
    signal overflow_i   : std_logic := '0';
    signal rd_data_i    : std_logic_vector(WIDTH-1 downto 0) := (others => '0');

    attribute ASYNC_REG : string;
    attribute ASYNC_REG of rd_ptr_gray_sync1_w : signal is "TRUE";
    attribute ASYNC_REG of rd_ptr_gray_sync2_w : signal is "TRUE";
    attribute ASYNC_REG of wr_ptr_gray_sync1_r : signal is "TRUE";
    attribute ASYNC_REG of wr_ptr_gray_sync2_r : signal is "TRUE";
begin

    assert DEPTH >= 4 report "async_sample_fifo DEPTH must be >= 4" severity failure;
    assert is_power_of_two(DEPTH)
        report "async_sample_fifo DEPTH must be a power of two"
        severity failure;

    full <= full_i;
    overflow <= overflow_i;
    empty <= empty_i;
    rd_data <= signed(rd_data_i);

    -- Synchronize the read pointer into the write clock domain.
    sync_rd_ptr : process(wr_clock)
    begin
        if rising_edge(wr_clock) then
            if wr_reset = '1' then
                rd_ptr_gray_sync1_w <= (others => '0');
                rd_ptr_gray_sync2_w <= (others => '0');
            else
                rd_ptr_gray_sync1_w <= rd_ptr_gray;
                rd_ptr_gray_sync2_w <= rd_ptr_gray_sync1_w;
            end if;
        end if;
    end process;

    -- Synchronize the write pointer into the read clock domain.
    sync_wr_ptr : process(rd_clock)
    begin
        if rising_edge(rd_clock) then
            if rd_reset = '1' then
                wr_ptr_gray_sync1_r <= (others => '0');
                wr_ptr_gray_sync2_r <= (others => '0');
            else
                wr_ptr_gray_sync1_r <= wr_ptr_gray;
                wr_ptr_gray_sync2_r <= wr_ptr_gray_sync1_r;
            end if;
        end if;
    end process;

    -- Write-side state.
    write_side : process(wr_clock)
        variable wr_ptr_next_bin  : unsigned(PTR_BITS-1 downto 0);
        variable wr_ptr_next_gray : unsigned(PTR_BITS-1 downto 0);
    begin
        if rising_edge(wr_clock) then
            if wr_reset = '1' then
                wr_ptr_bin <= (others => '0');
                wr_ptr_gray <= (others => '0');
                full_i <= '0';
                overflow_i <= '0';
            else
                wr_ptr_next_bin := wr_ptr_bin;

                if wr_en = '1' then
                    if full_i = '0' then
                        mem(to_integer(wr_ptr_bin(ADDR_BITS-1 downto 0))) <= std_logic_vector(wr_data);
                        wr_ptr_next_bin := wr_ptr_bin + 1;
                        wr_ptr_bin <= wr_ptr_next_bin;
                        wr_ptr_gray <= bin_to_gray(wr_ptr_next_bin);
                    else
                        overflow_i <= '1';
                    end if;
                end if;

                wr_ptr_next_gray := bin_to_gray(wr_ptr_next_bin + 1);
                if (wr_ptr_next_gray(PTR_BITS-1) /= rd_ptr_gray_sync2_w(PTR_BITS-1)) and
                   (wr_ptr_next_gray(PTR_BITS-2) /= rd_ptr_gray_sync2_w(PTR_BITS-2)) and
                   (wr_ptr_next_gray(PTR_BITS-3 downto 0) = rd_ptr_gray_sync2_w(PTR_BITS-3 downto 0)) then
                    full_i <= '1';
                else
                    full_i <= '0';
                end if;
            end if;
        end if;
    end process;

    -- Read-side state.
    read_side : process(rd_clock)
        variable rd_ptr_next_bin : unsigned(PTR_BITS-1 downto 0);
    begin
        if rising_edge(rd_clock) then
            if rd_reset = '1' then
                rd_ptr_bin <= (others => '0');
                rd_ptr_gray <= (others => '0');
                rd_data_i <= (others => '0');
                empty_i <= '1';
            else
                rd_ptr_next_bin := rd_ptr_bin;

                if rd_en = '1' and empty_i = '0' then
                    rd_ptr_next_bin := rd_ptr_bin + 1;
                    rd_ptr_bin <= rd_ptr_next_bin;
                    rd_ptr_gray <= bin_to_gray(rd_ptr_next_bin);
                end if;

                if wr_ptr_gray_sync2_r /= bin_to_gray(rd_ptr_next_bin) then
                    rd_data_i <= mem(to_integer(rd_ptr_next_bin(ADDR_BITS-1 downto 0)));
                end if;

                if wr_ptr_gray_sync2_r = bin_to_gray(rd_ptr_next_bin) then
                    empty_i <= '1';
                else
                    empty_i <= '0';
                end if;
            end if;
        end if;
    end process;

end architecture;
