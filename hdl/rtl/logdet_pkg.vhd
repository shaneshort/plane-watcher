-- =============================================================================
-- logdet_pkg.vhd -- Log-detector frontend parameters
-- =============================================================================
--
-- Holds constants specific to the log-detector RF chain
-- (AD8318 → AD8009 → AD9238 → FPGA). Separate from adsb_pkg.vhd so the
-- frontend-specific parameters (ADC clock rate, channel wiring) stay with
-- the frontend and the decode-core package keeps its focus on pipeline
-- timing, thresholds, and sample-width definitions.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

package logdet_pkg is

    -- ADC encode clock rate driven by the fabric MMCM onto the AD9238 CLK pin.
    --
    -- Final design target is 65 MHz (AD9238-65 datasheet maximum), giving a
    -- 4.0625:1 decimation to the 16 MHz decode rate. A conservative value is
    -- used for breadboard bring-up where signal integrity on flying wires
    -- limits the safe edge rate.
    --
    -- Changing this value requires regenerating the Clocking Wizard IP
    -- instance that produces the encode clock — the MMCM configuration is
    -- baked into the IP, not read from this package at elaboration time.
    constant ENCODE_CLK_HZ : integer := 16_000_000;

end package;
