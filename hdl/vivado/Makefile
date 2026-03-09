VIVADO ?= vivado
PROJECT_DIR ?= build/plane_watcher
PROJECT_FILE := $(PROJECT_DIR)/plane_watcher.xpr
PART ?= xc7z020clg400-2
VENDOR_HDL_ROOT ?=
BD_NAME ?= zynq_adsb
VENDOR_BD_NAME ?= zynq_adsb_vendor

JOBS ?= 16
BUILD_ID ?= 0
DEEP_DEBUG ?= 0

.PHONY: project bd vendor-bd bd-top vendor-top synth vendor-synth impl vendor-impl vendor-build stock-tap-build rtl clean

project:
	mkdir -p $(PROJECT_DIR)
	$(VIVADO) -mode batch -source create_project.tcl -tclargs $(PROJECT_DIR) $(PART) $(VENDOR_HDL_ROOT)

bd: project
	$(VIVADO) -mode batch -source create_zynq_bd.tcl -tclargs $(PROJECT_FILE) 0x43D00000

vendor-bd: project
	$(VIVADO) -mode batch -source create_vendor_hybrid_bd.tcl -tclargs $(PROJECT_FILE) $(VENDOR_HDL_ROOT) 0x43D00000

bd-top: bd
	$(VIVADO) -mode batch -source prepare_bd_top.tcl -tclargs $(PROJECT_FILE) $(BD_NAME)

vendor-top: vendor-bd
	$(VIVADO) -mode batch -source prepare_bd_top.tcl -tclargs $(PROJECT_FILE) $(VENDOR_BD_NAME)

synth: bd-top
	$(VIVADO) -mode batch -source run_synth.tcl -tclargs $(PROJECT_FILE)

vendor-synth: vendor-top
	$(VIVADO) -mode batch -source run_synth.tcl -tclargs $(PROJECT_FILE)

impl: bd-top
	$(VIVADO) -mode batch -source run_impl.tcl -tclargs $(PROJECT_FILE)

vendor-impl: vendor-top
	$(VIVADO) -mode batch -source run_impl.tcl -tclargs $(PROJECT_FILE)

# Single-session full build (saves ~2 min over the multi-step flow)
vendor-build:
	rm -rf $(PROJECT_DIR)
	$(VIVADO) -mode batch -source build_vendor.tcl -tclargs $(VENDOR_HDL_ROOT) 0x43D00000 $(JOBS) $(DEEP_DEBUG)

stock-tap-build:
	rm -rf $(PROJECT_DIR)
	$(VIVADO) -mode batch -source build_stock_tap.tcl -tclargs $(VENDOR_HDL_ROOT) 0x43D00000 $(JOBS) $(BUILD_ID) $(DEEP_DEBUG)

# Fast RTL-only rebuild — skips BD creation (requires prior full build)
rtl:
	$(VIVADO) -mode batch -source rebuild_rtl.tcl -tclargs $(JOBS)

clean:
	rm -rf $(PROJECT_DIR)
