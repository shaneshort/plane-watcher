-- =============================================================================
-- ad9203_ingress.vhd -- Parallel CMOS capture for AD9203 10-bit ADC
-- =============================================================================
--
-- Latches the 10-bit parallel CMOS data bus from the AD9203 ADC on the
-- Plane Watcher prototype frontend board.
-- The FPGA drives the encode clock via a fabric PLL, so the data outputs
-- are synchronous to the fabric clock domain — no CDC is required.
--
-- The AD9203 is a 10-bit, 40 MSPS, 3 V CMOS ADC. A single input register
-- stage gives a clean timing boundary for downstream logic.
--
-- The out-of-range (OTR) flag is captured and exposed for debug monitoring.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity ad9203_ingress is
    generic (
        ADC_WIDTH : positive := 10
    );
    port (
        clock     : in  std_logic;
        reset     : in  std_logic;

        -- AD9203 parallel output bus.
        adc_data  : in  std_logic_vector(ADC_WIDTH-1 downto 0);
        adc_otr   : in  std_logic;

        -- Registered outputs.
        out_data  : out unsigned(ADC_WIDTH-1 downto 0);
        out_otr   : out std_logic;
        out_valid : out std_logic
    );
end entity;

architecture rtl of ad9203_ingress is

    -- Input register stage. The AD9203 outputs are stable on the rising edge
    -- of the encode clock (which is this fabric clock). One register stage
    -- gives a clean timing boundary for downstream logic.
    signal data_r : std_logic_vector(ADC_WIDTH-1 downto 0) := (others => '0');
    signal otr_r  : std_logic := '0';

    -- Valid flag. Asserted one cycle after reset deasserts, stays high
    -- continuously — the ADC produces a sample every clock cycle.
    signal valid_r  : std_logic := '0';

begin

    process(clock, reset)
    begin
        if reset = '1' then
            data_r  <= (others => '0');
            otr_r   <= '0';
            valid_r  <= '0';
        elsif rising_edge(clock) then
            data_r  <= adc_data;
            otr_r   <= adc_otr;
            valid_r  <= '1';
        end if;
    end process;

    -- The prototype ties AD9203 DFS for straight-binary output. Expose the
    -- code as unsigned and let the downstream LUT handle RF calibration.
    out_data  <= unsigned(data_r);
    out_otr   <= otr_r;
    out_valid  <= valid_r;

end architecture;
