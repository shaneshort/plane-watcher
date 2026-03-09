# Pre-Hardware FPGA Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a simulatable ADS-B decode pipeline in VHDL with hardware timestamping, adapted from the bladeRF-adsb reference, verifiable before Zynq hardware arrives.

**Architecture:** Port the bladeRF-adsb VHDL decode pipeline (preamble detection → N parallel decoders → soft-decision bit-flip error correction → CRC), parameterise it via a central package, add a 64-bit timestamp counter with PPS capture, and thread timestamps through the pipeline. Validate with GHDL simulation against Python-generated test vectors.

**Tech Stack:** VHDL-2008, GHDL (simulation), GTKWave (waveforms), Python 3 via uv (test vector generation)

**Reference:** `bladeRF-adsb/` submodule contains the source VHDL. Design doc at `docs/plans/2026-03-10-pre-hardware-fpga-design.md`.

---

## Task 1: Install Simulation Tooling

**Files:** None (system setup)

**Step 1: Install GHDL and GTKWave via Homebrew**

Run:
```bash
brew install ghdl gtkwave
```

**Step 2: Verify GHDL works**

Run:
```bash
ghdl --version
```

Expected: Version string, no errors.

**Step 3: Initialise Python project with uv**

Run:
```bash
cd "/Volumes/External Storage/Documents/development/plane_watcher"
uv init --no-readme tools
```

This creates `tools/pyproject.toml`.

**Step 4: Add numpy dependency**

Run:
```bash
cd "/Volumes/External Storage/Documents/development/plane_watcher/tools"
uv add numpy
```

**Step 5: Verify Python environment**

Run:
```bash
cd "/Volumes/External Storage/Documents/development/plane_watcher/tools"
uv run python -c "import numpy; print(numpy.__version__)"
```

Expected: numpy version printed, no errors.

**Step 6: Commit**

```bash
git add tools/pyproject.toml tools/uv.lock tools/.python-version
git commit -m "feat: initialise Python tooling with uv + numpy"
```

---

## Task 2: Create Directory Structure and Makefile

**Files:**
- Create: `hdl/rtl/` (directory)
- Create: `hdl/tb/` (directory)
- Create: `hdl/sim/Makefile`
- Create: `hdl/sim/waves/` (directory)

**Step 1: Create directories**

Run:
```bash
cd "/Volumes/External Storage/Documents/development/plane_watcher"
mkdir -p hdl/rtl hdl/tb hdl/sim/waves
```

**Step 2: Write the simulation Makefile**

Create `hdl/sim/Makefile`:

```makefile
# GHDL Simulation Makefile for plane_watcher ADS-B decoder
#
# Usage:
#   make analyse       - Compile all VHDL sources
#   make sim_crc       - Run CRC testbench
#   make sim_timestamp - Run timestamp counter testbench
#   make sim_full      - Run full pipeline testbench
#   make sim_all       - Run all testbenches
#   make vectors       - Generate test vectors
#   make clean         - Remove build artifacts

GHDL       ?= ghdl
GHDL_FLAGS  = --std=08 --workdir=work
GTKWAVE    ?= gtkwave

RTL_DIR    = ../rtl
TB_DIR     = ../tb
WORK_DIR   = work
WAVE_DIR   = waves
TOOLS_DIR  = ../../tools
VECTORS_DIR = vectors

# RTL sources in dependency order
RTL_SRCS = \
	$(RTL_DIR)/adsb_pkg.vhd \
	$(RTL_DIR)/timestamp_counter.vhd \
	$(RTL_DIR)/adsb_crc.vhd \
	$(RTL_DIR)/smallest_bsds.vhd \
	$(RTL_DIR)/bit_flipper.vhd \
	$(RTL_DIR)/bsd_calculator.vhd \
	$(RTL_DIR)/adsb_edge_detector.vhd \
	$(RTL_DIR)/preamble_detector.vhd \
	$(RTL_DIR)/message_decoder.vhd \
	$(RTL_DIR)/message_aggregator.vhd \
	$(RTL_DIR)/adsb_decoder.vhd

# Testbench sources
TB_SRCS = \
	$(TB_DIR)/adsb_crc_tb.vhd \
	$(TB_DIR)/timestamp_counter_tb.vhd

.PHONY: all analyse clean vectors sim_crc sim_timestamp sim_all

all: analyse

$(WORK_DIR):
	mkdir -p $(WORK_DIR)

$(VECTORS_DIR):
	mkdir -p $(VECTORS_DIR)

analyse: $(WORK_DIR)
	$(GHDL) -a $(GHDL_FLAGS) $(RTL_SRCS)
	$(GHDL) -a $(GHDL_FLAGS) $(TB_SRCS)

# --- Testbench targets ---

sim_crc: analyse
	$(GHDL) -e $(GHDL_FLAGS) adsb_crc_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_crc_tb --wave=$(WAVE_DIR)/crc.ghw

sim_timestamp: analyse
	$(GHDL) -e $(GHDL_FLAGS) timestamp_counter_tb
	$(GHDL) -r $(GHDL_FLAGS) timestamp_counter_tb --wave=$(WAVE_DIR)/timestamp.ghw

sim_all: sim_crc sim_timestamp

# --- Waveform viewing ---

waves_crc: sim_crc
	$(GTKWAVE) $(WAVE_DIR)/crc.ghw &

waves_timestamp: sim_timestamp
	$(GTKWAVE) $(WAVE_DIR)/timestamp.ghw &

# --- Test vector generation ---

vectors: $(VECTORS_DIR)
	cd $(TOOLS_DIR) && uv run python gen_test_vectors.py --output-dir ../hdl/sim/$(VECTORS_DIR)

# --- Cleanup ---

clean:
	rm -rf $(WORK_DIR) $(VECTORS_DIR)
	rm -f *.ghw *.cf
	rm -f $(WAVE_DIR)/*.ghw
```

**Step 3: Add .gitkeep to empty dirs**

Run:
```bash
touch hdl/sim/waves/.gitkeep
```

**Step 4: Commit**

```bash
git add hdl/
git commit -m "feat: add directory structure and GHDL simulation Makefile"
```

---

## Task 3: Create `adsb_pkg.vhd` — Central Package

**Files:**
- Create: `hdl/rtl/adsb_pkg.vhd`
- Reference: `bladeRF-adsb/vhdl/adsb_decoder_p.vhd` (original package)

**Step 1: Write the package**

Create `hdl/rtl/adsb_pkg.vhd`:

```vhdl
library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

package adsb_pkg is

    -- Sample rate and timing
    constant SAMPLE_RATE_HZ    : positive := 16_000_000;
    constant SPS               : positive := 8;       -- samples per PPM chip
    constant SPB               : positive := 2;       -- chips per bit (PPM)

    -- Decoder configuration
    constant NUM_DECODERS      : positive := 8;       -- parallel decoders (Gold Mode)
    constant NUM_WEAK_BITS     : positive := 5;       -- bits for brute-force correction

    -- Signal widths
    constant INPUT_POWER_WIDTH : positive := 24;
    constant COUNTER_WIDTH     : positive := 64;

    -- Preamble timing (derived from SPS)
    constant PREAMBLE_BITS         : positive := 8;
    constant PREAMBLE_BUFFER_LENGTH: positive := SPS * SPB * PREAMBLE_BITS;

    -- Types
    type messages_t is array(natural range <>) of std_logic_vector(111 downto 0);

    type adsb_message_t is record
        data        : std_logic_vector(111 downto 0);
        timestamp   : unsigned(COUNTER_WIDTH-1 downto 0);
        fractional  : unsigned(15 downto 0);
        rpl         : signed(INPUT_POWER_WIDTH-1 downto 0);
        valid       : std_logic;
    end record;

    type adsb_messages_t is array(natural range <>) of adsb_message_t;

    -- Constants for preamble detection
    constant POWER_THRESHOLD : signed(INPUT_POWER_WIDTH-1 downto 0) :=
        to_signed(5000, INPUT_POWER_WIDTH);

    constant EDGE_POWER_THRESHOLD : signed(INPUT_POWER_WIDTH-1 downto 0) :=
        to_signed(100, INPUT_POWER_WIDTH);

end package;
```

**Step 2: Verify it compiles**

Run:
```bash
cd hdl/sim
make analyse
```

Expected: GHDL will fail on missing files for the other sources listed in the Makefile. That's expected — we only have the package so far. Instead, compile just the package:

```bash
cd hdl/sim && mkdir -p work
ghdl -a --std=08 --workdir=work ../rtl/adsb_pkg.vhd
```

Expected: No errors.

**Step 3: Commit**

```bash
git add hdl/rtl/adsb_pkg.vhd
git commit -m "feat: add adsb_pkg.vhd central package with parameterised constants"
```

---

## Task 4: Implement `timestamp_counter.vhd`

**Files:**
- Create: `hdl/rtl/timestamp_counter.vhd`
- Reference: Design doc section "New Module: timestamp_counter.vhd"

**Step 1: Write the timestamp counter**

Create `hdl/rtl/timestamp_counter.vhd`:

```vhdl
library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity timestamp_counter is
    generic (
        WIDTH : positive := COUNTER_WIDTH
    );
    port (
        clock          : in  std_logic;
        reset          : in  std_logic;

        -- Current counter value (active every cycle)
        counter_value  : out unsigned(WIDTH-1 downto 0);

        -- PPS interface
        pps_in         : in  std_logic;
        counter_at_pps : out unsigned(WIDTH-1 downto 0);
        pps_count      : out unsigned(31 downto 0);
        pps_new        : out std_logic
    );
end entity;

architecture rtl of timestamp_counter is

    signal count       : unsigned(WIDTH-1 downto 0) := (others => '0');
    signal pps_sr      : std_logic_vector(2 downto 0) := (others => '0');
    signal pps_rising  : std_logic;
    signal pps_cnt     : unsigned(31 downto 0) := (others => '0');
    signal pps_latch   : unsigned(WIDTH-1 downto 0) := (others => '0');

begin

    -- Double-flop synchroniser + rising edge detect
    pps_rising <= pps_sr(1) and not pps_sr(2);

    process(clock)
    begin
        if rising_edge(clock) then
            if reset = '1' then
                count      <= (others => '0');
                pps_sr     <= (others => '0');
                pps_cnt    <= (others => '0');
                pps_latch  <= (others => '0');
                pps_new    <= '0';
            else
                -- Free-running counter
                count <= count + 1;

                -- PPS synchroniser: shift in raw PPS
                pps_sr <= pps_sr(1 downto 0) & pps_in;

                -- PPS event
                if pps_rising = '1' then
                    pps_latch <= count;
                    pps_cnt   <= pps_cnt + 1;
                    pps_new   <= '1';
                else
                    pps_new   <= '0';
                end if;
            end if;
        end if;
    end process;

    counter_value  <= count;
    counter_at_pps <= pps_latch;
    pps_count      <= pps_cnt;

end architecture;
```

**Step 2: Compile to check syntax**

Run:
```bash
cd hdl/sim
ghdl -a --std=08 --workdir=work ../rtl/adsb_pkg.vhd ../rtl/timestamp_counter.vhd
```

Expected: No errors.

**Step 3: Commit**

```bash
git add hdl/rtl/timestamp_counter.vhd
git commit -m "feat: add timestamp_counter with 64-bit counter and PPS capture"
```

---

## Task 5: Write `timestamp_counter_tb.vhd` — Testbench

**Files:**
- Create: `hdl/tb/timestamp_counter_tb.vhd`

**Step 1: Write the testbench**

Create `hdl/tb/timestamp_counter_tb.vhd`:

```vhdl
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
        generic map (
            WIDTH => COUNTER_WIDTH
        )
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
        -- Wait for N rising edges
        procedure wait_clocks(n : positive) is
        begin
            for i in 1 to n loop
                wait until rising_edge(clock);
            end loop;
        end procedure;
    begin
        -- Hold reset
        reset <= '1';
        wait_clocks(10);
        reset <= '0';
        wait_clocks(5);

        -- Test 1: Counter should be incrementing
        report "Test 1: Counter incrementing";
        assert counter_value > to_unsigned(0, COUNTER_WIDTH)
            report "Counter should be non-zero after reset released"
            severity failure;

        -- Record current value and verify it advances
        wait_clocks(10);
        assert counter_value > to_unsigned(15, COUNTER_WIDTH)
            report "Counter should have advanced past 15"
            severity failure;
        report "Test 1: PASSED";

        -- Test 2: PPS capture
        report "Test 2: PPS capture";
        assert pps_count = to_unsigned(0, 32)
            report "PPS count should be 0 before any PPS"
            severity failure;

        -- Simulate a PPS pulse
        wait_clocks(100);
        pps_in <= '1';
        -- Hold PPS high for a few clocks (realistic pulse width)
        wait_clocks(10);
        pps_in <= '0';

        -- Wait for synchroniser latency (3 flops + 1 edge detect)
        wait_clocks(5);

        assert pps_count = to_unsigned(1, 32)
            report "PPS count should be 1 after first PPS, got " &
                   integer'image(to_integer(pps_count))
            severity failure;

        assert counter_at_pps > to_unsigned(0, COUNTER_WIDTH)
            report "counter_at_pps should be non-zero after PPS"
            severity failure;

        report "Test 2: PASSED — PPS latched at counter = " &
               integer'image(to_integer(counter_at_pps(31 downto 0)));

        -- Test 3: Second PPS updates latch
        report "Test 3: Second PPS event";
        wait_clocks(1000);

        pps_in <= '1';
        wait_clocks(10);
        pps_in <= '0';
        wait_clocks(5);

        assert pps_count = to_unsigned(2, 32)
            report "PPS count should be 2 after second PPS"
            severity failure;

        report "Test 3: PASSED — Second PPS latched at counter = " &
               integer'image(to_integer(counter_at_pps(31 downto 0)));

        -- Test 4: pps_new is single-cycle pulse
        report "Test 4: pps_new pulse width";
        wait_clocks(500);

        pps_in <= '1';
        wait_clocks(10);
        pps_in <= '0';

        -- Wait for synchroniser
        wait until pps_new = '1' for 20 * CLK_PERIOD;
        assert pps_new = '1'
            report "pps_new should have pulsed"
            severity failure;

        wait until rising_edge(clock);
        assert pps_new = '0'
            report "pps_new should be single-cycle pulse"
            severity failure;

        report "Test 4: PASSED";

        -- Done
        report "== All timestamp_counter tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;

end architecture;
```

**Step 2: Compile and run**

Run:
```bash
cd hdl/sim
ghdl -a --std=08 --workdir=work ../rtl/adsb_pkg.vhd ../rtl/timestamp_counter.vhd ../tb/timestamp_counter_tb.vhd
ghdl -e --std=08 --workdir=work timestamp_counter_tb
ghdl -r --std=08 --workdir=work timestamp_counter_tb --wave=waves/timestamp.ghw
```

Expected: All 4 tests print PASSED. Simulation ends with "All timestamp_counter tests PASSED".

**Step 3: Commit**

```bash
git add hdl/tb/timestamp_counter_tb.vhd
git commit -m "test: add timestamp_counter testbench — 4 tests covering increment, PPS latch, pulse width"
```

---

## Task 6: Port `adsb_crc.vhd`

**Files:**
- Create: `hdl/rtl/adsb_crc.vhd`
- Reference: `bladeRF-adsb/vhdl/adsb_crc.vhd`

**Step 1: Port the CRC module**

Copy from bladeRF and adapt: replace the `work.adsb_decoder_p` reference with `work.adsb_pkg`. The CRC logic itself is unchanged — the module doesn't depend on any bladeRF-specific types.

Create `hdl/rtl/adsb_crc.vhd`:

```vhdl
-- ADS-B CRC-24 calculator
-- Ported from bladeRF-adsb (Nuand) with package reference updated.
-- Polynomial: x^24 + x^23 + ... + x^3 + 1 (0x1FFF409)
-- Handles both short (56-bit) and extended (112-bit) messages.

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity adsb_crc is
    port (
        clock      : in  std_logic;
        reset      : in  std_logic;

        busy       : out std_logic;

        data       : in  std_logic_vector(111 downto 0);
        data_valid : in  std_logic;

        crc        : out std_logic_vector(23 downto 0);
        crc_good   : out std_logic;
        crc_valid  : out std_logic
    );
end entity;

architecture rtl of adsb_crc is

    constant CRC_POLY : std_logic_vector(24 downto 0) := 25x"1fff409";

    type fsm_t is (IDLE, CALCULATING, DONE);

    type state_t is record
        fsm     : fsm_t;
        count   : natural range 0 to 14-1;
        data    : std_logic_vector(111 downto 0);
        crc     : std_logic_vector(24 downto 0);
        busy    : std_logic;
        valid   : std_logic;
        good    : std_logic;
        nonzero : std_logic;
    end record;

    signal current, future : state_t;

begin

    sync : process(clock, reset)
    begin
        if reset = '1' then
            current.fsm  <= IDLE;
            current.busy <= '1';
        elsif rising_edge(clock) then
            current <= future;
        end if;
    end process;

    comb : process(all)
        variable crc_v : std_logic_vector(current.crc'range);
    begin
        future <= current;
        case current.fsm is
            when IDLE =>
                future.busy    <= '0';
                future.valid   <= '0';
                future.good    <= '0';
                future.nonzero <= '0';
                if data_valid = '1' then
                    future.data <= data;
                    future.fsm  <= CALCULATING;
                    future.crc  <= (others => '0');
                    future.busy <= '1';
                    -- Bit 7 of first byte (MSB of DF field) determines
                    -- short (7 bytes) vs extended (14 bytes)
                    if data(7) = '1' then
                        future.count <= 14-1;
                    else
                        future.count <= 7-1;
                    end if;
                end if;

            when CALCULATING =>
                if current.nonzero = '0' and
                   unsigned(current.data(7 downto 0)) /= 0 then
                    future.nonzero <= '1';
                end if;
                future.data <= x"00" & current.data(current.data'high downto 8);
                crc_v := current.crc;
                for i in 7 downto 0 loop
                    crc_v := crc_v(crc_v'high-1 downto 0) & current.data(i);
                    if crc_v(crc_v'high) = '1' then
                        crc_v := crc_v xor CRC_POLY;
                    end if;
                end loop;
                future.crc <= crc_v;
                if current.count = 0 then
                    future.fsm <= DONE;
                else
                    future.count <= current.count - 1;
                end if;

            when DONE =>
                future.fsm   <= IDLE;
                future.valid <= '1';
                if to_integer(unsigned(current.crc)) = 0 and
                   current.nonzero = '1' then
                    future.good <= '1';
                else
                    future.good <= '0';
                end if;
                future.busy <= '0';

            when others =>
                future.fsm <= IDLE;

        end case;
    end process;

    busy      <= current.busy;
    crc_good  <= current.good;
    crc_valid <= current.valid;
    crc       <= current.crc(crc'range);

end architecture;
```

**Step 2: Compile**

Run:
```bash
cd hdl/sim
ghdl -a --std=08 --workdir=work ../rtl/adsb_pkg.vhd ../rtl/adsb_crc.vhd
```

Expected: No errors.

**Step 3: Commit**

```bash
git add hdl/rtl/adsb_crc.vhd
git commit -m "feat: port adsb_crc from bladeRF-adsb — CRC-24 with short/extended support"
```

---

## Task 7: Write `adsb_crc_tb.vhd` — CRC Testbench

**Files:**
- Create: `hdl/tb/adsb_crc_tb.vhd`
- Reference: `bladeRF-adsb/vhdl/tb/adsb_crc_tb.vhd`, known message from bladeRF code

**Step 1: Write the CRC testbench**

The bladeRF code contains a known-good message: `0xd001f7a69b7ecff20f584b80758d` (112 bits). We test that CRC passes for this message, and fails for a corrupted version.

Create `hdl/tb/adsb_crc_tb.vhd`:

```vhdl
library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity adsb_crc_tb is
end entity;

architecture tb of adsb_crc_tb is

    constant CLK_PERIOD : time := 62.5 ns;  -- 16 MHz

    signal clock      : std_logic := '0';
    signal reset      : std_logic := '1';
    signal data       : std_logic_vector(111 downto 0) := (others => '0');
    signal data_valid : std_logic := '0';
    signal busy       : std_logic;
    signal crc        : std_logic_vector(23 downto 0);
    signal crc_good   : std_logic;
    signal crc_valid  : std_logic;

    signal sim_done : boolean := false;

    -- Byte-swap a 112-bit vector from big-endian hex to the bit ordering
    -- expected by the CRC block (LSB-first byte feeding)
    function swizzle(x : std_logic_vector(111 downto 0))
        return std_logic_vector
    is
        variable rv : std_logic_vector(111 downto 0);
        constant n  : integer := 14;
    begin
        for i in 0 to n-1 loop
            rv((n-i)*8-1 downto (n-i-1)*8) := x((i+1)*8-1 downto i*8);
        end loop;
        return rv;
    end function;

    -- Known good extended message from bladeRF-adsb source
    -- Raw hex (big-endian): 8D75804B580FF2CF7E9BA6 + CRC
    -- Full 14-byte frame as stored in bladeRF code:
    constant GOOD_MSG : std_logic_vector(111 downto 0) :=
        112x"d001f7a69b7ecff20f584b80758d";

begin

    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.adsb_crc
        port map (
            clock      => clock,
            reset      => reset,
            busy       => busy,
            data       => data,
            data_valid => data_valid,
            crc        => crc,
            crc_good   => crc_good,
            crc_valid  => crc_valid
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

        -- Test 1: Known good message should pass CRC
        report "Test 1: Known good message";
        data       <= GOOD_MSG;
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';

        -- Wait for CRC to complete
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        assert crc_good = '1'
            report "CRC should pass for known good message"
            severity failure;
        report "Test 1: PASSED — Good message CRC verified";

        wait_clocks(5);

        -- Test 2: Corrupted message should fail CRC
        report "Test 2: Corrupted message";
        -- Flip one bit in the message
        data       <= GOOD_MSG xor (112x"000000000000000000000000_0002");
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';

        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        assert crc_good = '0'
            report "CRC should fail for corrupted message"
            severity failure;
        report "Test 2: PASSED — Corrupted message rejected";

        wait_clocks(5);

        -- Test 3: All-zero message should fail (nonzero check)
        report "Test 3: All-zero message";
        data       <= (others => '0');
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';

        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        assert crc_good = '0'
            report "CRC should fail for all-zero message"
            severity failure;
        report "Test 3: PASSED — All-zero message rejected";

        report "== All adsb_crc tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;

end architecture;
```

**Step 2: Compile and run**

Run:
```bash
cd hdl/sim
ghdl -a --std=08 --workdir=work ../rtl/adsb_pkg.vhd ../rtl/adsb_crc.vhd ../tb/adsb_crc_tb.vhd
ghdl -e --std=08 --workdir=work adsb_crc_tb
ghdl -r --std=08 --workdir=work adsb_crc_tb --wave=waves/crc.ghw
```

Expected: All 3 tests PASSED.

**Step 3: Commit**

```bash
git add hdl/tb/adsb_crc_tb.vhd
git commit -m "test: add adsb_crc testbench — known good, corrupted, and zero message tests"
```

---

## Task 8: Write `gen_test_vectors.py` — Test Vector Generator

**Files:**
- Create: `tools/gen_test_vectors.py`

**Step 1: Write the test vector generator**

This script encodes known ADS-B messages as I/Q sample files that the VHDL testbenches can read. Based on the MATLAB model in `bladeRF-adsb/matlab/adsb_out.m`.

Create `tools/gen_test_vectors.py`:

```python
#!/usr/bin/env python3
"""Generate ADS-B I/Q test vectors for VHDL simulation.

Encodes known ADS-B messages as Mode-S PPM waveforms at a configurable
sample rate, with optional noise and multi-message support. Outputs binary
files compatible with the VHDL testbench file I/O (16-bit signed I, 16-bit
signed Q, little-endian).

Reference: bladeRF-adsb/matlab/adsb_out.m
"""

import argparse
import struct
from pathlib import Path

import numpy as np


# Mode-S preamble: 1010 0001 0100 0000 (8 us at 1 MHz chip rate = 16 chips)
PREAMBLE_CHIPS = np.array([1, 0, 1, 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0],
                          dtype=np.float64)

# CRC-24 polynomial for Mode-S
CRC_POLY = 0x1FFF409


def crc24(bits: np.ndarray) -> np.ndarray:
    """Compute CRC-24 for Mode-S and return 24-bit remainder as array."""
    crc = 0
    for b in bits:
        crc = (crc << 1) | int(b)
        if crc & (1 << 24):
            crc ^= CRC_POLY
    return np.array([(crc >> (23 - i)) & 1 for i in range(24)], dtype=np.int32)


def hex_to_bits(hex_str: str) -> np.ndarray:
    """Convert hex string to numpy array of bits (MSB first)."""
    n_bits = len(hex_str) * 4
    val = int(hex_str, 16)
    return np.array([(val >> (n_bits - 1 - i)) & 1 for i in range(n_bits)],
                    dtype=np.int32)


def bits_to_ppm(bits: np.ndarray) -> np.ndarray:
    """Encode bit array as PPM chips (2 chips per bit)."""
    chips = np.zeros(len(bits) * 2, dtype=np.float64)
    for i, b in enumerate(bits):
        if b:
            chips[2 * i] = 1.0
            chips[2 * i + 1] = 0.0
        else:
            chips[2 * i] = 0.0
            chips[2 * i + 1] = 1.0
    return chips


def encode_adsb_message(hex_msg: str, sps: int = 8) -> np.ndarray:
    """Encode hex ADS-B message (without CRC) into I/Q samples.

    Parameters
    ----------
    hex_msg : str
        Hex string of message content. For extended (DF>=16): 11 bytes
        (DF+CA+ICAO+ME = 88 bits). For short: 4 bytes (DF+CA+ICAO = 32 bits).
    sps : int
        Samples per PPM chip.

    Returns
    -------
    np.ndarray
        Complex I/Q samples (float64).
    """
    msg_bits = hex_to_bits(hex_msg)
    crc_bits = crc24(msg_bits)
    payload = np.concatenate([msg_bits, crc_bits])

    # PPM encode
    data_chips = bits_to_ppm(payload)

    # Full frame: preamble + data
    frame_chips = np.concatenate([PREAMBLE_CHIPS, data_chips])

    # Upsample to target sample rate
    frame_up = np.repeat(frame_chips, sps)

    # Convert to complex I/Q (signal on both I and Q, like bladeRF MATLAB model)
    iq = frame_up + 1j * frame_up

    return iq


def generate_test_file(
    messages: list[dict],
    output_path: Path,
    sps: int = 8,
    noise_amplitude: float = 0.0,
    dead_air_samples: int = 1000,
    amplitude: float = 0.5,
    seed: int = 42,
) -> None:
    """Generate a binary I/Q test vector file.

    Parameters
    ----------
    messages : list[dict]
        List of dicts with keys: 'hex' (message hex), 'offset' (sample offset,
        optional), 'amplitude' (per-message amplitude, optional).
    output_path : Path
        Output file path.
    sps : int
        Samples per chip.
    noise_amplitude : float
        Gaussian noise amplitude (0.0 = no noise).
    dead_air_samples : int
        Silence samples before first message and after last.
    amplitude : float
        Default signal amplitude (0.0 to 1.0, scaled to 12-bit range).
    seed : int
        Random seed for reproducibility.
    """
    rng = np.random.default_rng(seed)

    # Encode all messages
    encoded = []
    for msg in messages:
        iq = encode_adsb_message(msg['hex'], sps=sps)
        msg_amp = msg.get('amplitude', amplitude)
        iq *= msg_amp
        offset = msg.get('offset', None)
        encoded.append((iq, offset))

    # Calculate total length
    if encoded[0][1] is None:
        # Sequential: dead_air + msg1 + dead_air + msg2 + ... + dead_air
        total_len = dead_air_samples
        offsets = []
        for iq, _ in encoded:
            offsets.append(total_len)
            total_len += len(iq) + dead_air_samples
    else:
        # Explicit offsets
        max_end = 0
        offsets = []
        for iq, offset in encoded:
            offsets.append(offset)
            end = offset + len(iq)
            if end > max_end:
                max_end = end
        total_len = max_end + dead_air_samples

    # Build output buffer
    output = np.zeros(total_len, dtype=np.complex128)
    for (iq, _), offset in zip(encoded, offsets):
        end = offset + len(iq)
        output[offset:end] += iq

    # Add noise
    if noise_amplitude > 0:
        noise = rng.normal(0, noise_amplitude, total_len) + \
                1j * rng.normal(0, noise_amplitude, total_len)
        output += noise

    # Scale to 12-bit ADC range (-2048 to 2047) and clamp
    scale = 2047.0
    i_samples = np.clip(np.round(output.real * scale), -2048, 2047).astype(np.int16)
    q_samples = np.clip(np.round(output.imag * scale), -2048, 2047).astype(np.int16)

    # Write as interleaved 16-bit signed little-endian (matches bladeRF TB format)
    with open(output_path, 'wb') as f:
        for i_val, q_val in zip(i_samples, q_samples):
            f.write(struct.pack('<hh', int(i_val), int(q_val)))

    n_bytes = total_len * 4
    print(f"Wrote {output_path}: {total_len} samples, {n_bytes} bytes")
    for i, msg in enumerate(messages):
        print(f"  Message {i}: {msg['hex']} at offset {offsets[i]}")


def main():
    parser = argparse.ArgumentParser(description='Generate ADS-B test vectors')
    parser.add_argument('--output-dir', type=Path, default=Path('.'),
                        help='Output directory for test vector files')
    parser.add_argument('--sps', type=int, default=8,
                        help='Samples per chip (default: 8)')
    args = parser.parse_args()

    args.output_dir.mkdir(parents=True, exist_ok=True)

    # --- Test vector 1: Single clean extended message ---
    # DF17 CA5 ICAO:75804B ME:580FF2CF7E9BA6
    # This is the known-good message from the bladeRF-adsb source
    generate_test_file(
        messages=[{'hex': '8D75804B580FF2CF7E9BA6'}],
        output_path=args.output_dir / 'single_clean.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        amplitude=0.5,
    )

    # --- Test vector 2: Single message with noise ---
    generate_test_file(
        messages=[{'hex': '8D75804B580FF2CF7E9BA6'}],
        output_path=args.output_dir / 'single_noisy.dat',
        sps=args.sps,
        noise_amplitude=0.02,
        amplitude=0.5,
    )

    # --- Test vector 3: Two non-overlapping messages ---
    generate_test_file(
        messages=[
            {'hex': '8D75804B580FF2CF7E9BA6'},
            {'hex': '8D4840D6202CC371C32CE0'},
        ],
        output_path=args.output_dir / 'two_sequential.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        amplitude=0.5,
    )

    # --- Test vector 4: Two overlapping messages (collision test) ---
    # Second message starts partway through the first, at lower amplitude
    msg1_len = (16 + 112 * 2) * args.sps  # preamble + extended data in samples
    generate_test_file(
        messages=[
            {'hex': '8D75804B580FF2CF7E9BA6', 'offset': 1000, 'amplitude': 0.5},
            {'hex': '8D4840D6202CC371C32CE0', 'offset': 1000 + msg1_len // 2,
             'amplitude': 0.3},
        ],
        output_path=args.output_dir / 'collision.dat',
        sps=args.sps,
        noise_amplitude=0.01,
        amplitude=0.5,
    )

    # --- Test vector 5: Short message (DF=0, 56-bit) ---
    # DF0 = 00000, so first byte starts with 0b000xxxxx
    # Using a synthetic short message: DF=0, rest zeros + CRC
    generate_test_file(
        messages=[{'hex': '02E19504'}],
        output_path=args.output_dir / 'single_short.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        amplitude=0.5,
    )

    print("\nAll test vectors generated.")


if __name__ == '__main__':
    main()
```

**Step 2: Run and verify**

Run:
```bash
cd "/Volumes/External Storage/Documents/development/plane_watcher/tools"
uv run python gen_test_vectors.py --output-dir ../hdl/sim/vectors
```

Expected: 5 `.dat` files created with summary output.

**Step 3: Add vectors directory to .gitignore**

Create or update `.gitignore` at repo root:
```
hdl/sim/vectors/
hdl/sim/work/
hdl/sim/waves/*.ghw
*.cf
```

**Step 4: Commit**

```bash
git add tools/gen_test_vectors.py .gitignore
git commit -m "feat: add Python test vector generator — clean, noisy, sequential, collision, short message vectors"
```

---

## Task 9: Port Remaining Decode Pipeline Modules

This task ports the remaining bladeRF VHDL modules. Each is a focused copy-and-adapt.

**Files:**
- Create: `hdl/rtl/adsb_edge_detector.vhd`
- Create: `hdl/rtl/preamble_detector.vhd`
- Create: `hdl/rtl/bsd_calculator.vhd`
- Create: `hdl/rtl/smallest_bsds.vhd`
- Create: `hdl/rtl/bit_flipper.vhd`
- Create: `hdl/rtl/message_decoder.vhd`
- Create: `hdl/rtl/message_aggregator.vhd`
- Create: `hdl/rtl/adsb_decoder.vhd`
- Reference: Corresponding files in `bladeRF-adsb/vhdl/`

For each module, the adaptation pattern is:

1. Copy from `bladeRF-adsb/vhdl/<module>.vhd`
2. Replace `use work.adsb_decoder_p.all` with `use work.adsb_pkg.all`
3. Parameterise hardcoded `SPS` (= 8) to use the package constant
4. For `preamble_detector`: add `counter_value` input port and latch timestamp on detection
5. For `message_decoder`: thread timestamp from SOM to output
6. For `message_aggregator`: expand output record to include timestamp + RPL
7. For `adsb_decoder`: add `counter_value` input, connect to preamble detector

**Step 1: Port all modules**

Each module follows the same pattern. The key changes beyond package references:

**`preamble_detector.vhd`** — Add ports:
```vhdl
counter_value : in unsigned(COUNTER_WIDTH-1 downto 0);
toa_out       : out unsigned(COUNTER_WIDTH-1 downto 0);
```
Latch `counter_value` into `toa_out` when `preamble_detected` goes high.

**`message_decoder.vhd`** — Add ports:
```vhdl
toa_in    : in  unsigned(COUNTER_WIDTH-1 downto 0);
toa_out   : out unsigned(COUNTER_WIDTH-1 downto 0);
```
Register `toa_in` on SOM and forward to output.

**`message_aggregator.vhd`** — Change output to carry timestamp:
```vhdl
out_message   : out std_logic_vector(127 downto 0);  -- existing
out_timestamp : out unsigned(COUNTER_WIDTH-1 downto 0);
out_rpl       : out signed(INPUT_POWER_WIDTH-1 downto 0);
```

**`adsb_decoder.vhd`** — Add `counter_value` input, wire through to preamble detector.

All other modules (`adsb_edge_detector`, `bsd_calculator`, `smallest_bsds`, `bit_flipper`) are near drop-in with only the package reference change and SPS parameterisation.

**Step 2: Compile all modules**

Run:
```bash
cd hdl/sim
make analyse
```

Expected: All sources compile without errors.

**Step 3: Commit**

```bash
git add hdl/rtl/
git commit -m "feat: port bladeRF-adsb decode pipeline — edge detect, preamble, BSD, bit-flip, CRC, multi-decoder

Adapted from bladeRF-adsb with:
- Centralised adsb_pkg parameters
- Timestamp threading through preamble → decoder → aggregator
- SPS parameterised (default 8 for 16 MSPS)"
```

---

## Task 10: Verify Full Pipeline with `make sim_all`

**Files:**
- Modify: `hdl/sim/Makefile` (ensure all TB targets work)

**Step 1: Run all testbenches**

```bash
cd hdl/sim
make clean
make vectors
make sim_all
```

Expected: All testbenches pass. Test vectors generated.

**Step 2: Inspect waveforms for sanity**

```bash
make waves_timestamp
```

Visually verify:
- Counter incrementing
- PPS latch capturing correct value
- pps_new single-cycle pulse

**Step 3: Commit any Makefile adjustments**

```bash
git add hdl/sim/Makefile
git commit -m "chore: finalise Makefile targets for full simulation flow"
```

---

## Summary

| Task | What | Depends On |
|------|------|-----------|
| 1 | Install GHDL, GTKWave, uv+numpy | — |
| 2 | Directory structure + Makefile | Task 1 |
| 3 | `adsb_pkg.vhd` | Task 2 |
| 4 | `timestamp_counter.vhd` | Task 3 |
| 5 | `timestamp_counter_tb.vhd` | Task 4 |
| 6 | Port `adsb_crc.vhd` | Task 3 |
| 7 | `adsb_crc_tb.vhd` | Task 6 |
| 8 | `gen_test_vectors.py` | Task 1 |
| 9 | Port remaining decode pipeline | Tasks 3, 4, 6 |
| 10 | Full pipeline verification | Tasks 5, 7, 8, 9 |

Tasks 4-5 and 6-7 can run in parallel. Task 8 can run in parallel with 4-7.
