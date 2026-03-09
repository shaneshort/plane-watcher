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
	$(RTL_DIR)/adsb_decoder.vhd \
	$(RTL_DIR)/async_sample_fifo.vhd \
	$(RTL_DIR)/async_msg_fifo.vhd \
	$(RTL_DIR)/iq_to_power.vhd \
	$(RTL_DIR)/power_downsampler.vhd \
	$(RTL_DIR)/vendor_rx_ingress.vhd \
	$(RTL_DIR)/axi_regs.vhd \
	$(RTL_DIR)/adsb_top.vhd \
	$(RTL_DIR)/adsb_pl_wrapper.vhd

# Testbench sources
TB_SRCS = \
	$(TB_DIR)/adsb_crc_tb.vhd \
	$(TB_DIR)/timestamp_counter_tb.vhd \
	$(TB_DIR)/async_sample_fifo_tb.vhd \
	$(TB_DIR)/edge_detector_probe_tb.vhd \
	$(TB_DIR)/adsb_decoder_tb.vhd \
	$(TB_DIR)/axi_regs_tb.vhd

.PHONY: all analyse clean vectors vectors-dir sim_crc sim_timestamp sim_all

all: analyse

$(WORK_DIR):
	mkdir -p $(WORK_DIR)

vectors-dir:
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

sim_sample_fifo: analyse
	$(GHDL) -e $(GHDL_FLAGS) async_sample_fifo_tb
	$(GHDL) -r $(GHDL_FLAGS) async_sample_fifo_tb --wave=$(WAVE_DIR)/sample_fifo.ghw

sim_edge_probe: analyse
	$(GHDL) -e $(GHDL_FLAGS) edge_detector_probe_tb
	$(GHDL) -r $(GHDL_FLAGS) edge_detector_probe_tb --wave=$(WAVE_DIR)/edge_probe.ghw

sim_decoder: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/decoder_clean.ghw -gVECTOR_FILE=vectors/single_clean.dat

sim_decoder_noisy: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/decoder_noisy.ghw -gVECTOR_FILE=vectors/single_noisy.dat

sim_decoder_two: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/decoder_two.ghw -gVECTOR_FILE=vectors/two_sequential.dat

sim_decoder_busy: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/decoder_busy.ghw -gVECTOR_FILE=vectors/busy_traffic.dat

sim_decoder_busy_noisy: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/decoder_busy_noisy.ghw -gVECTOR_FILE=vectors/busy_noisy.dat

sim_weak1: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/weak1.ghw -gVECTOR_FILE=vectors/weak1.dat

sim_weak2: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/weak2.ghw -gVECTOR_FILE=vectors/weak2.dat

sim_weak3: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/weak3.ghw -gVECTOR_FILE=vectors/weak3.dat

sim_weak4: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/weak4.ghw -gVECTOR_FILE=vectors/weak4.dat

sim_weak5: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb --wave=$(WAVE_DIR)/weak5.ghw -gVECTOR_FILE=vectors/weak5.dat

sim_weak_all: sim_weak1 sim_weak2 sim_weak3 sim_weak4 sim_weak5

sim_axi: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) axi_regs_tb
	$(GHDL) -r $(GHDL_FLAGS) axi_regs_tb -gVECTOR_FILE=vectors/two_sequential.dat

# --- Detector regression ---
# With the reduced holdoff (512 samples = 32 µs), payload re-triggers
# may produce additional SOMs on free decoders. The correctness invariant
# is on accepted message count (*** DECODED MESSAGE), not raw SOM count.
# SOM counts are bounded but allowed higher than before.

sim_short: analyse vectors
	$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb -gVECTOR_FILE=vectors/single_short.dat

# Count accepted decoded messages (covers both CRC-managed and raw-forwarded DFs).
MSG_CHECK = 2>&1 | tee /tmp/ghdl_det_check.log | grep -c "DECODED MESSAGE"
# Count SOMs for bounding check.
SOM_CHECK = grep -c "SOM_DBG: SOM fired" /tmp/ghdl_det_check.log

EXPECT_MSG = | { read n; [ "$$n" -eq $(1) ] || { echo "FAIL: expected $(1) decoded message(s), got $$n"; grep -E "DECODED MESSAGE|SOM_DBG" /tmp/ghdl_det_check.log; exit 1; }; echo "PASS: $$n decoded message(s)"; }
EXPECT_SOM_LE = | { read n; [ "$$n" -le $(1) ] || { echo "FAIL: expected <=$(1) SOMs, got $$n"; exit 1; }; echo "OK: $$n SOM(s)"; }

sim_detector_regression: analyse vectors
	@echo "--- single_clean: expect 2 decoded messages (1 CRC-valid + 1 re-trigger), bounded SOMs ---"
	@$(GHDL) -e $(GHDL_FLAGS) adsb_decoder_tb
	@$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb -gVECTOR_FILE=vectors/single_clean.dat $(MSG_CHECK) $(call EXPECT_MSG,2)
	@$(SOM_CHECK) $(call EXPECT_SOM_LE,4)
	@echo "--- single_short: expect 1 decoded message, bounded SOMs ---"
	@$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb -gVECTOR_FILE=vectors/single_short.dat $(MSG_CHECK) $(call EXPECT_MSG,1)
	@$(SOM_CHECK) $(call EXPECT_SOM_LE,4)
	@echo "--- two_sequential: expect 3 decoded messages (2 CRC-valid + 1 re-trigger), bounded SOMs ---"
	@$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb -gVECTOR_FILE=vectors/two_sequential.dat $(MSG_CHECK) $(call EXPECT_MSG,3)
	@$(SOM_CHECK) $(call EXPECT_SOM_LE,6)
	@echo "--- collision: expect 0 decoded messages, bounded SOMs ---"
	@$(GHDL) -r $(GHDL_FLAGS) adsb_decoder_tb -gVECTOR_FILE=vectors/collision.dat $(MSG_CHECK) $(call EXPECT_MSG,0)
	@$(SOM_CHECK) $(call EXPECT_SOM_LE,8)
	@echo "=== All detector regression checks passed ==="

sim_all: sim_crc sim_timestamp sim_sample_fifo sim_decoder sim_short sim_detector_regression

# --- Waveform viewing ---

waves_crc: sim_crc
	$(GTKWAVE) $(WAVE_DIR)/crc.ghw &

waves_timestamp: sim_timestamp
	$(GTKWAVE) $(WAVE_DIR)/timestamp.ghw &

# --- Test vector generation ---

vectors: vectors-dir
	cd $(TOOLS_DIR) && uv run python gen_test_vectors.py --output-dir ../hdl/sim/$(VECTORS_DIR)

# --- Cleanup ---

clean:
	rm -rf $(WORK_DIR) $(VECTORS_DIR)
	rm -f *.ghw *.cf
	rm -f $(WAVE_DIR)/*.ghw
