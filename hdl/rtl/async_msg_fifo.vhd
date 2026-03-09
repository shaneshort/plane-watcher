-- =============================================================================
-- async_msg_fifo.vhd -- Dual-clock FIFO for sample-to-AXI clock crossing
-- =============================================================================
--
-- Crosses decoded message records from the sample clock domain into the AXI
-- clock domain using Gray-coded read/write pointers with two-flop pointer
-- synchronization. The read output (rd_data) is registered in the read
-- clock domain, so new data appears one cycle after a pointer advance or
-- after the FIFO transitions from empty to non-empty. Storage is hinted
-- as block RAM (ram_style = "block") to keep the read path off the
-- critical timing path.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity async_msg_fifo is
    generic (
        WIDTH : positive := 200;
        DEPTH : positive := 64
    );
    port (
        reset    : in  std_logic;

        -- Write side
        wr_clock : in  std_logic;
        wr_data  : in  std_logic_vector(WIDTH-1 downto 0);
        wr_en    : in  std_logic;
        full     : out std_logic;

        -- Read side
        rd_clock : in  std_logic;
        rd_data  : out std_logic_vector(WIDTH-1 downto 0);
        rd_en    : in  std_logic;
        empty    : out std_logic;

        -- Read-domain status
        count    : out unsigned(6 downto 0);
        overflow : out std_logic
    );
end entity;

architecture rtl of async_msg_fifo is
    constant ADDR_BITS : positive := 6;  -- log2(64)
    constant PTR_BITS  : positive := ADDR_BITS + 1;

    type memory_t is array(0 to DEPTH-1) of std_logic_vector(WIDTH-1 downto 0);
    signal mem : memory_t;
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

    signal rd_ptr_bin_sync_w   : unsigned(PTR_BITS-1 downto 0) := (others => '0');
    signal wr_ptr_bin_sync_r   : unsigned(PTR_BITS-1 downto 0) := (others => '0');

    signal full_i              : std_logic := '0';
    signal empty_i             : std_logic := '1';
    signal rd_data_i           : std_logic_vector(WIDTH-1 downto 0) := (others => '0');

    signal overflow_w          : std_logic := '0';
    signal overflow_sync1_r    : std_logic := '0';
    signal overflow_sync2_r    : std_logic := '0';

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

begin

    rd_data <= rd_data_i;

    full  <= full_i;
    empty <= empty_i;
    count <= resize(wr_ptr_bin_sync_r - rd_ptr_bin, count'length);
    overflow <= overflow_sync2_r;

    -- Synchronize read pointer into write clock domain.
    process(wr_clock, reset)
    begin
        if reset = '1' then
            rd_ptr_gray_sync1_w <= (others => '0');
            rd_ptr_gray_sync2_w <= (others => '0');
            rd_ptr_bin_sync_w   <= (others => '0');
        elsif rising_edge(wr_clock) then
            rd_ptr_gray_sync1_w <= rd_ptr_gray;
            rd_ptr_gray_sync2_w <= rd_ptr_gray_sync1_w;
            rd_ptr_bin_sync_w   <= gray_to_bin(rd_ptr_gray_sync2_w);
        end if;
    end process;

    -- Synchronize write pointer and overflow flag into read clock domain.
    process(rd_clock, reset)
    begin
        if reset = '1' then
            wr_ptr_gray_sync1_r <= (others => '0');
            wr_ptr_gray_sync2_r <= (others => '0');
            wr_ptr_bin_sync_r   <= (others => '0');
            overflow_sync1_r    <= '0';
            overflow_sync2_r    <= '0';
        elsif rising_edge(rd_clock) then
            wr_ptr_gray_sync1_r <= wr_ptr_gray;
            wr_ptr_gray_sync2_r <= wr_ptr_gray_sync1_r;
            wr_ptr_bin_sync_r   <= gray_to_bin(wr_ptr_gray_sync2_r);
            overflow_sync1_r    <= overflow_w;
            overflow_sync2_r    <= overflow_sync1_r;
        end if;
    end process;

    -- Write-side state.
    process(wr_clock, reset)
        variable wr_ptr_next_bin  : unsigned(PTR_BITS-1 downto 0);
        variable wr_ptr_next_gray : unsigned(PTR_BITS-1 downto 0);
    begin
        if reset = '1' then
            wr_ptr_bin  <= (others => '0');
            wr_ptr_gray <= (others => '0');
            full_i      <= '0';
            overflow_w  <= '0';
        elsif rising_edge(wr_clock) then
            wr_ptr_next_bin := wr_ptr_bin;

            if wr_en = '1' then
                if full_i = '0' then
                    mem(to_integer(wr_ptr_bin(ADDR_BITS-1 downto 0))) <= wr_data;
                    wr_ptr_next_bin := wr_ptr_bin + 1;
                    wr_ptr_bin <= wr_ptr_next_bin;
                    wr_ptr_gray <= bin_to_gray(wr_ptr_next_bin);
                else
                    overflow_w <= '1';
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
    end process;

    -- Read-side state.
    process(rd_clock, reset)
        variable rd_ptr_next_bin : unsigned(PTR_BITS-1 downto 0);
    begin
        if reset = '1' then
            rd_ptr_bin  <= (others => '0');
            rd_ptr_gray <= (others => '0');
            empty_i     <= '1';
            rd_data_i   <= (others => '0');
        elsif rising_edge(rd_clock) then
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
    end process;

end architecture;
