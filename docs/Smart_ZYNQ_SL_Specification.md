# Smart ZYNQ SL Edition — Resource & Specification Document

> **Source:** [hellofpga.com — Smart ZYNQ (SL Edition) Resource Summary](http://www.hellofpga.com/index.php/2023/05/10/smart-zynq-sl/)
> **Last updated:** April 13, 2026
> **Vendor/Shop:** 杭海电子科技 (Hanghai Electronics Technology) — available on Taobao

> **Note:** This page covers the **Smart ZYNQ SL Edition** (screenless variant). For the Standard, SP, or SP2 editions, refer to their respective documentation pages.

---

## Table of Contents

1. [Board Overview](#1-board-overview)
2. [Hardware Specifications](#2-hardware-specifications)
3. [Hardware Version Changelog](#3-hardware-version-changelog)
4. [Downloadable Resources](#4-downloadable-resources)
5. [Important Notes & Cautions](#5-important-notes--cautions)
6. [Pre-loaded Demo Program](#6-pre-loaded-demo-program)
7. [Test Firmware (TF Card)](#7-test-firmware-tf-card)
8. [Tutorial Index](#8-tutorial-index)
   - [8.1 Board-Related Resources](#81-board-related-resources)
   - [8.2 PL (FPGA Logic) Experiments](#82-pl-fpga-logic-experiments)
   - [8.3 PS (ARM Processor) Experiments](#83-ps-arm-processor-experiments)
   - [8.4 LVGL Experiments](#84-lvgl-experiments)
   - [8.5 Petalinux Tutorials](#85-petalinux-tutorials)
   - [8.6 PYNQ Tutorials](#86-pynq-tutorials)
   - [8.7 Xillinux (Graphical OS) Tutorials](#87-xillinux-graphical-os-tutorials)
9. [Known Issues / Community Q&A](#9-known-issues--community-qa)

---

## 1. Board Overview

The Smart ZYNQ SL ("Screenless") is a compact ZYNQ development board based on the Xilinx XC7Z020 SoC. It is the screenless variant of the Smart ZYNQ family, sharing the same pin layout and GPIO routing as the SP and SP2 editions.

**Compatibility note:** Programs designed for the SP or SP2 editions are **fully compatible** with the SL edition, and vice versa, with the exception of features that the SL board does not carry — namely USB HOST, USB SLAVE, and the onboard LCD screen.

---

## 2. Hardware Specifications

| Feature | Detail |
|---|---|
| **Main Chip** | Xilinx ZYNQ XC7Z020-CLG484 |
| **PCB Layers** | 8-layer (ENIG / immersion gold finish) |
| **DDR Memory** | 256M × 16-bit (512 MB) |
| **Flash** | 128 Mbit (16 MB) — connected to PS side for boot |
| **EEPROM** | 24C02, 2 kbit |
| **TF Card Slot** | microSD slot (supports system boot) |
| **PS Clock** | 33.33 MHz active crystal oscillator |
| **PL Clock** | 50 MHz active crystal oscillator |
| **Ethernet** | 1× Gigabit Ethernet via RTL8211E (connected to PL; also usable by PS) |
| **HDMI** | 1× HDMI output (IO-simulated) |
| **UART** | 1× USB-UART (connected to PL side; also usable by PS) |
| **Buttons** | 2× user buttons (PL side) + 1× POR hardware reset button |
| **LEDs** | 2× programmable LEDs (PL side) + 1× Done indicator LED + 1× power indicator LED |
| **Boot DIP Switch** | Dual-position DIP switch for boot mode selection |
| **Logic IO** | 68 FPGA IOs total, all routed as differential pairs (length-matched within pairs) — all from PL side; accessible from PS via EMIO mapping |
| **RGB LCD Interface** | 40-pin RGB LCD connector (40P FPC) added in **V1.3B** — compatible with this site's and Zhengdian Atom's RGB screens |
| **Adjustable IO Voltage** | One bank's GPIO voltage is independently adjustable via a 0805 resistor (RA/RB on the PCB back) |
| **Power Input** | Via Type-C port (JTAG or USB Slave), or via VCC pin on header (5 V external) |
| **Programmer** | **No onboard programmer** — requires an external Xilinx JTAG programmer (2.54 mm pitch, 5×2 PIN connector) |

---

## 3. Hardware Version Changelog

Version information is printed on the silkscreen near the POR button.

### V1.3B (March 26, 2026)
- Added a **40-pin RGB LCD interface** (FPC connector, compatible with Zhengdian Atom RGB screens; includes 35 FPGA IO pins)
- All other dimensions and features are identical to V1.3

### V1.3 / V1.2 (October 2024)
- **Header power pin relocation:** Power pin position on one side of the headers was adjusted so that both sides of the board accept the same peripheral modules. ⚠️ **V1.2/V1.3 headers are NOT pin-compatible with V1.0/V1.1.**
- **Power IC change:** TPS563201 (TI) replaced with TPS563210A (includes PG signal)
- **V1.3 only — HDMI CLK pin change:** HDMI clock pin changed from N22 to N19 (now uses SRCC clock pin for compatibility with third-party HDMI IP cores that require SRCC). Reserved HDMI I2C signals and HDMI_RX_HPD signal also added.

### Version Comparison Diagram

```
V1.0 / V1.1
  └── Original design

V1.2
  └── Adjusted header power pin position
  └── Power IC: TPS563201 → TPS563210A (adds PG signal)

V1.3
  └── Same as V1.2, plus:
  └── HDMI CLK: N22 → N19 (SRCC pin)
  └── Added reserved HDMI I2C and HPD signals

V1.3B
  └── Same as V1.3, plus:
  └── Added 40-pin RGB LCD (FPC) interface
```

---

## 4. Downloadable Resources

All files are hosted on hellofpga.com.

| Resource | Description | Link |
|---|---|---|
| **Schematic V1.3B** (latest, covers V1.0–V1.3B with annotations) | PDF schematic, updated April 13, 2026 | [SmartZynq_SL_Schematic_V1d3B_20260413.pdf](http://www.hellofpga.com/wp-content/uploads/2023/05/SmartZynq_SL_Schematic_V1d3B_20260413.pdf.pdf) |
| **Schematic V1.3** | PDF schematic | [SmartZynq_SL_Schematic_V1d3_20241005.pdf](http://www.hellofpga.com/wp-content/uploads/2023/05/SmartZynq_SL_Schematic_V1d3_20241005.pdf) |
| **Schematic V1.2** | PDF schematic | [Smart_ZYNQ_SL_V1D2_Schematic_20240719.pdf](http://www.hellofpga.com/wp-content/uploads/2023/05/Smart_ZYNQ_SL_V1D2_Schematic_20240719.pdf) |
| **Schematic V1.0 / V1.1** | PDF schematic | [Smart_ZYNQ_SL_Schematic_20230510.pdf](http://www.hellofpga.com/wp-content/uploads/2023/05/Smart_ZYNQ_SL_Schematic_20230510.pdf) |
| **Dimension Drawing** | Board outline & dimensions (PDF) | [Smart_ZYNQ_SL_SIZE_20230510.pdf](http://www.hellofpga.com/wp-content/uploads/2023/05/Smart_ZYNQ_SL_SIZE_20230510.pdf) |
| **IO Pin Length Report** | Trace length report for differential pairs on headers (XLS) | [Smart_ZYNQ_SL_IO_length_20230704.xls](http://www.hellofpga.com/wp-content/uploads/2023/05/Smart_ZYNQ_SL_IO_length_20230704.xls) |
| **Component Datasheets** | Datasheets for all major ICs on the board (ZIP) | [smart_zynq_datasheet.zip](http://www.hellofpga.com/wp-content/uploads/2024/01/smart_zynq_datasheet.zip) |
| **Acrylic Enclosure DXF** | Laser-cut acrylic cover drawing for Smart ZYNQ SL | [Smart Zynq SL Acrylic Drawing](http://www.hellofpga.com/index.php/2024/05/14/smart-zynq-sl-wk/) |

---

## 5. Important Notes & Cautions

1. **BANK33 IO Voltage Adjustment**
   BANK33 voltage is adjustable by changing two 0805 resistors on the back of the board (silkscreened **RA** and **RB**). Factory default is **3.3 V**. Theoretically adjustable down to 1.2 V, though signal quality at 1.2 V through header pins may be degraded in practice.

2. **USB HOST and USB SLAVE share the same ZYNQ USB resource** — only one function can be used at a time. Do not connect devices to both simultaneously. When using USB HOST mode, power the board via a USB charger/adapter — not a PC — if you also want to supply power through the USB Slave port.

3. **Power supply options:** Both Type-C ports can power the board. The VCC pin on the headers can also accept 5 V. USB and header supply do not conflict (USB is diode-protected for reverse-current protection). If using a desktop PC's front-panel USB port causes instability, try the rear USB port which typically has lower line resistance.

4. **USB current limits:** Some laptop/PC USB ports are limited to 500 mA at 5 V. This is sufficient for the board alone, but if peripherals or extension modules are attached, consider supplying 5 V separately via the header VCC pin. Always connect peripherals with the board powered off, then power on.

5. **Header VCC pin is bidirectional:**
   - *As input:* Accepts 5 V from an external source (e.g. carrier board). Note that DuPont wire connections may result in slightly higher voltage drop.
   - *As output:* When powered only by USB, outputs approximately 4.5–4.7 V (to supply a carrier board).

6. **POR RST button** is a full hardware reset with the highest priority. Pressing it will unconditionally restart the entire system according to the configured boot mode, overriding any running software.

---

## 6. Pre-loaded Demo Program

The board ships with a demo program pre-flashed to QSPI Flash. To run it, set the boot DIP switch to **QSPI FLASH** mode and power on.

| Function | Behaviour |
|---|---|
| HDMI Output | Displays a 720p colour bar test pattern |
| UART | Sends `"hello world"` once per second |
| LEDs | Running-light (chase) pattern after power-on; pressing KEY1 or KEY2 stops the chase and lights the corresponding LED |

---

## 7. Test Firmware (TF Card)

The test firmware supports **all Smart ZYNQ SL versions V1.0 through V1.3**.

**How to use:** Download and extract the file, copy the contents to the root directory of a FAT32-formatted TF card, insert the card, set the DIP switch to **SD boot**, and press the POR reset button.

### Firmware 1 — Full-Function Bare-Metal Test (GPIO + UART + KEY + LED)

| Function | Behaviour |
|---|---|
| GPIO | All header output GPIO pins toggle level every 1 second |
| HDMI | 720p colour bar output |
| UART | Sends `"hello world"` every 1 second |
| LEDs | Running-light pattern; stops and lights corresponding LED on KEY1/KEY2 press |

**Download:** [Smart_ZYNQ_SP2_SL_ALL_TEST_20240916.zip](http://www.hellofpga.com/wp-content/uploads/2024/01/Smart_ZYNQ_SP2_SL_ALL_TEST_20240916.zip)

### Firmware 2 — Full-Function Linux Test (GPIO + UART + KEY + LED + NET)

| Function | Behaviour |
|---|---|
| GPIO | All header output GPIO pins toggle level every 1 second |
| HDMI | 720p colour bar output |
| UART | Outputs a Linux command-line terminal (viewable via PuTTY or similar) |
| LEDs | Running-light pattern; stops and lights corresponding LED on KEY1/KEY2 press |
| Network | Connect via Ethernet cable to a router; confirmation visible in serial terminal; supports ping to router and other network devices |

**Download:** [Smart_ZYNQ_SP2_LINUX_ALL_TEST_20240906.zip](http://www.hellofpga.com/wp-content/uploads/2024/01/Smart_ZYNQ_SP2_LINUX_ALL_TEST_20240906.zip)

---

## 8. Tutorial Index

> All tutorials on this site are based on **Vivado 2018.3**. It is strongly recommended to use this version if you are new to the workflow.

### 8.1 Board-Related Resources

- [Note on XC7020 chip markings/sanding](http://www.hellofpga.com/index.php/2023/07/05/mark/)
- [Smart Zynq SL Acrylic Enclosure DXF Drawing](http://www.hellofpga.com/index.php/2024/05/14/smart-zynq-sl-wk/)
- [ADB_ZD02 — Zhengdian Atom Module Adapter Board (camera, AD/DA modules; includes Gerber files)](http://www.hellofpga.com/index.php/2024/04/20/adb_zd02/)

### 8.2 PL (FPGA Logic) Experiments

| # | Title | Link |
|---|---|---|
| — | Resolving ZYNQ SDK debug failures | [Link](http://www.hellofpga.com/index.php/2022/10/28/10280957/) |
| — | Methods for adding reset signals to ZYNQ PL programs | [Link](http://www.hellofpga.com/index.php/2023/01/08/zynq_pl_rst/) |
| — | Vivado 2018.3 download and installation | [Link](http://www.hellofpga.com/index.php/2023/03/22/vivado-2018-3/) |
| — | Verilog quick-start notes | [Link](http://www.hellofpga.com/index.php/2023/04/06/verilog_01/) |
| — | Vivado 2019.2+ with Vitis — brief tutorial (optional) | [Link](http://www.hellofpga.com/index.php/2023/11/04/vitis/) |
| 1 | Light an LED using ZYNQ PL resources (full walkthrough) | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynq_sp_sl_01_led/) |
| 2 | Running-light design using ZYNQ PL (FPGA) | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspsl_pl_led_water/) |
| 3 | Button input on PL side (IO input demo) | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspsl-key_test_pl/) |
| 4 | Vivado built-in simulation demo | [Link](http://www.hellofpga.com/index.php/2023/04/28/ila_test/) |
| 5 | FPGA hardware debugging — ILA demo | [Link](http://www.hellofpga.com/index.php/2023/04/28/ila_test-2/) |
| 6 | FPGA VIO online debug test | [Link](http://www.hellofpga.com/index.php/2023/04/28/vio_test-2/) |
| 7 | PWM demo using FPGA resources | [Link](http://www.hellofpga.com/index.php/2023/04/28/pwm_test/) |
| 8 | On-chip MMCM/PLL clock module test | [Link](http://www.hellofpga.com/index.php/2023/04/28/pll_test/) |
| 9 | On-chip Block RAM IP core usage | [Link](http://www.hellofpga.com/index.php/2023/04/28/bram_test/) |
| 10 | Reading pin voltages via on-chip XADC from PL side | [Link](http://www.hellofpga.com/index.php/2023/08/03/xadc_pin_test/) |
| 11 | Signal acquisition using dual high-speed ADC module | [Link](http://www.hellofpga.com/index.php/2024/06/15/dual_hd_adc_test/) |
| 12 | HDMI demo using ZYNQ PL resources | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspsl_hdmi_test/) |
| 13 | RGB888 screen demo via PL (5-inch 800×480 screen) | [Link](http://www.hellofpga.com/index.php/2024/12/08/sp_sl_lcd_demo/) |
| 14 | PL-side FPGA UART serial communication | [Link](http://www.hellofpga.com/index.php/2025/04/16/uart_test-2/) |
| 17 | Gigabit Ethernet UDP loopback experiment | [Link](http://www.hellofpga.com/index.php/2025/12/22/udp_net_test/) |
| PS-6 (supplement) | Burning a pure FPGA program to QSPI Flash | [Link](http://www.hellofpga.com/index.php/2023/08/29/fpga_qspi_flash/) |
| PS-16 | Using PS to provide clock to PL logic | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspampsl_clk_ps_pl/) |
| PS-13 | PS↔PL interaction: PS accessing PL register | [Link](http://www.hellofpga.com/index.php/2023/04/28/ps_to_pl_reg/) |
| PS-14 | PS↔PL interaction: PL reading/writing PS DDR | [Link](http://www.hellofpga.com/index.php/2023/09/14/pl_ddr_ps_test/) |
| PS-15 | PS↔PL interaction: PS accessing PL BRAM | [Link](http://www.hellofpga.com/index.php/2023/11/12/ps_bram_test/) |

### 8.3 PS (ARM Processor) Experiments

| # | Title | Link |
|---|---|---|
| 1 | GPIO: Light LED via EMIO (full walkthrough) | [Link](http://www.hellofpga.com/index.php/2023/04/28/smart_zynq_axi_gpio-2-2/) |
| 2 | GPIO: Light LED via AXI-GPIO (full walkthrough) | [Link](http://www.hellofpga.com/index.php/2023/04/28/smart_zynq_axi_gpio-2/) |
| 3 | GPIO: Button input demo (EMIO method) | [Link](http://www.hellofpga.com/index.php/2023/04/28/ps_gpio_in/) |
| 4 | External interrupt experiment | [Link](http://www.hellofpga.com/index.php/2023/04/28/ps_interrupt_test/) |
| 5 | Timer interrupt experiment | [Link](http://www.hellofpga.com/index.php/2023/04/28/ps_timer_test/) |
| 6 | Programming to QSPI Flash (Flash boot) | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspampsl_qspi_flash/) |
| 6 (supplement) | Pure FPGA program to QSPI Flash | [Link](http://www.hellofpga.com/index.php/2023/08/29/fpga_qspi_flash/) |
| 7 | TF card boot demo | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspampslsmart-zynq_tf_test/) |
| 8 | UART function demo | [Link](http://www.hellofpga.com/index.php/2023/04/28/ps_uart_test/) |
| 9 | QSPI Flash read/write test | [Link](http://www.hellofpga.com/index.php/2023/11/08/qspi_flash_test/) |
| 10 | Gigabit Ethernet initial test (EMIO) | [Link](http://www.hellofpga.com/index.php/2023/04/28/smart-zynq_net_test-2/) |
| 12 | Dual-core AMP experiment on ZYNQ PS | [Link](http://www.hellofpga.com/index.php/2024/03/17/amp_test/) |
| 13 | PS↔PL: PS accessing PL register | [Link](http://www.hellofpga.com/index.php/2023/04/28/ps_to_pl_reg/) |
| 14 | PS↔PL: PL reading/writing PS DDR | [Link](http://www.hellofpga.com/index.php/2023/09/14/pl_ddr_ps_test/) |
| 15 | PS↔PL: PS accessing PL BRAM | [Link](http://www.hellofpga.com/index.php/2023/11/12/ps_bram_test/) |
| 16 | PS providing clock to PL logic | [Link](http://www.hellofpga.com/index.php/2023/04/27/smart-zynqspampsl_clk_ps_pl/) |
| 17 | On-chip XADC: reading chip voltage and temperature | [Link](http://www.hellofpga.com/index.php/2023/06/17/xadc_test/) |
| 19 | VDMA image buffering to HDMI (Part 1): PS colour bar display | [Link](http://www.hellofpga.com/index.php/2023/05/11/vdma_01/) |
| 20 | VDMA image buffering to HDMI (Part 2): Display BMP from TF card | [Link](http://www.hellofpga.com/index.php/2023/05/15/vdma3/) |
| 21 | 5-inch RGB LCD (Part 1): VDMA colour bar display | [Link](http://www.hellofpga.com/index.php/2024/12/14/sp_sl_vdma_lcd_color_bar/) |
| 22 | 5-inch RGB LCD (Part 2): VDMA display BMP from TF card | [Link](http://www.hellofpga.com/index.php/2024/12/14/vdma_lcd_tf_image/) |
| 23 | 5-inch RGB LCD (Part 3): Capacitive touch panel demo | [Link](http://www.hellofpga.com/index.php/2024/12/18/sp_sl_lcd_touch_panel_demo/) |

### 8.4 LVGL Experiments

| Title | Link |
|---|---|
| LVGL v8.3.10 step-by-step porting tutorial for ZYNQ boards (VDMA + DMA method) | [Link](http://www.hellofpga.com/index.php/2025/01/01/lvgl_demo_01/) |

### 8.5 Petalinux Tutorials

#### Preparation

| Chapter | Title | Link |
|---|---|---|
| 1 | Ubuntu and VMware installation | [Link](http://www.hellofpga.com/index.php/2022/11/27/vmware/) |
| 1 (supplement) | Shared folder setup in Ubuntu (optional) | [Link](http://www.hellofpga.com/index.php/2022/11/27/gx/) |
| 2 | Vivado installation under Ubuntu (optional) | [Link](http://www.hellofpga.com/index.php/2023/08/17/ubuntu_vivado/) |
| 3 | Petalinux development environment installation | [Link](http://www.hellofpga.com/index.php/2022/11/28/petalinux/) |

#### Petalinux Development for Smart ZYNQ

| Chapter | Title | Link |
|---|---|---|
| 1 | Vivado base hardware project setup | [Link](http://www.hellofpga.com/index.php/2025/02/23/smart_zynq_petalinux_01/) |
| 2 | Petalinux project creation and full build on Ubuntu (TF card boot) | [Link](http://www.hellofpga.com/index.php/2025/02/23/smart_zynq_building_petalinux/) |
| 3 | Creating a bootable SD card (boot + rootfs partitions) | [Link](http://www.hellofpga.com/index.php/2025/02/23/zynq_creating_sd_card/) |
| 4 | Boot verification | [Link](http://www.hellofpga.com/index.php/2025/02/25/petalinux_boot_test/) |
| 5 | SSH remote login to Petalinux | [Link](http://www.hellofpga.com/index.php/2025/02/23/zynq-petalinux-ssh/) |
| 6 | Remote file transfer via SCP from Windows | [Link](http://www.hellofpga.com/index.php/2025/02/23/petalinux_scp_test/) |
| 7 | GPIO input/output experiment | [Link](http://www.hellofpga.com/index.php/2025/02/28/petalinux_sys_gpio/) |
| 8 | GPIO control via application program | [Link](http://www.hellofpga.com/index.php/2023/04/30/petalinux_gpio_app-2/) |
| 9 | USB HOST experiment | ⚠️ **Not applicable to Smart ZYNQ SL** |
| 10 | Booting Linux from QSPI Flash | [Link](http://www.hellofpga.com/index.php/2023/04/28/zynq-linux_qspi_flash_uart-2/) |

> **Note:** When porting Petalinux 2022 or later, you may encounter a uboot hang requiring manual input of `run mmc_boot` to start the kernel. See [Technical FAQ post](http://www.hellofpga.com/index.php/2025/05/08/question/) (Issue #6) for the fix. This issue does not affect Petalinux 2018.3.

### 8.6 PYNQ Tutorials

> Status: Tested and functional. Images available in Chapter 3. Still being refined as of August 2023.

| Chapter | Title | Link |
|---|---|---|
| 1 | Development and porting environment setup | [Link](http://www.hellofpga.com/index.php/2023/08/18/pynq_part1/) |
| 2 | Vivado hardware project setup (Smart ZYNQ SP & SL) | [Link](http://www.hellofpga.com/index.php/2023/08/19/pynq_vivado/) |
| 3 | PYNQ image build (Smart ZYNQ SP & SL) — includes test image download | [Link](http://www.hellofpga.com/index.php/2023/08/21/pynq_img/) |

### 8.7 Xillinux (Graphical OS) Tutorials

> **Smart ZYNQ SL limitation:** Because the SL board has no USB port, it cannot access the graphical desktop directly. However, it can run Xillinux with limited functionality (command-line via serial or network). The desktop can be accessed remotely — see Chapter 14.

| Chapter | Title | Link |
|---|---|---|
| — | Xillinux OS resource summary | [Link](http://www.hellofpga.com/index.php/2023/10/12/xillinux/) |
| 1 | TF card preparation: burning the image | [Link](http://www.hellofpga.com/index.php/2023/10/16/xillinux-image/) |
| 2 | TF card preparation: using the demo bundle | [Link](http://www.hellofpga.com/index.php/2023/10/17/xillinux-demo-bundle/) |
| 3 | Xillinux boot verification | [Link](http://www.hellofpga.com/index.php/2023/10/18/xillinux-start/) — ⚠️ SL: no desktop; use Chapter 14 for remote desktop |
| 4 | Resizing the file system | [Link](http://www.hellofpga.com/index.php/2023/10/20/file-system/) |
| 5 | Setting a custom Ethernet MAC address (optional) | [Link](http://www.hellofpga.com/index.php/2023/10/31/mac-set/) |
| 6 | Using the onboard SPI LCD screen | [Link](http://www.hellofpga.com/index.php/2023/10/31/1d47-lcd/) — ⚠️ **Not applicable to SL (no screen)** |
| 7 | EEPROM read/write experiment | [Link](http://www.hellofpga.com/index.php/2023/11/01/eeprom/) |
| 8 | GPIO input/output experiment | [Link](http://www.hellofpga.com/index.php/2023/11/13/gpio_sysfs/) |
| 9 | SSH remote login to Xillinux | [Link](http://www.hellofpga.com/index.php/2023/11/15/ssh/) |
| 10 | Remote file transfer via SCP from Windows | [Link](http://www.hellofpga.com/index.php/2023/11/15/scp/) |
| 11 | Mount Windows shared folder via CIFS | [Link](http://www.hellofpga.com/index.php/2023/12/01/cifs/) |
| 12 | Set up Samba CIFS server for Windows file sharing | [Link](http://www.hellofpga.com/index.php/2023/12/10/cifs2/) |
| 13 | Auto-mount TF card partitions | [Link](http://www.hellofpga.com/index.php/2023/12/11/mount/) |
| 14 | Remote desktop: display and control Xillinux desktop from Windows | [Link](http://www.hellofpga.com/index.php/2023/12/24/x-window/) |
| 15 | Connect headphones to digital output pin and play audio | [Link](http://www.hellofpga.com/index.php/2024/03/02/pulse-width-modulation-audio/) |
| 16 | Real-time preview and video capture from OV7670 camera | [Link](http://www.hellofpga.com/index.php/2024/04/15/ov7670/) |
| 16 (supplement) | Configuring OV7670 sensor registers via I2C on Smart ZYNQ | [Link](http://www.hellofpga.com/index.php/2024/06/02/ov7670-2/) |

---

## 9. Known Issues / Community Q&A

The following questions and answers are drawn from the comment section of the original page.

**Q: UART receive interrupt is not triggering on this board, but works fine on other boards.**
> A: The UART uses EMIO mapping on this board, which should not affect interrupt behaviour. The issue is likely in the user's project configuration.

**Q: Can PS SPI be used on the Smart ZYNQ SL?**
> A: Yes. Use EMIO to map the PS SPI function to PL-side pins.

**Q: Why are VCC3V3 (3.3 V) rather than VCCIO_ADJ routed to pins 37 and 38 of the U12 connector?**
> A: 3.3 V is the most commonly used voltage. Many peripherals require 3.3 V for power even if their communication bus operates at 1.8 V. A VCCADJ output connector is available on the board near KEY1 for users who need the adjustable voltage.

**Q: The HDMI hot-plug circuit in the schematic appears to allow ZYNQ to control the HPD pin but not read its input state.**
> A: The HPD signal was not present in versions before V1.3. For HDMI output use, a continuous signal suffices and HPD detection is unnecessary. The HPD, I2C, and related signals added in V1.3 are reserved for future HDMI input functionality (demo to be released).

**Q: Which hardware version does the IO length report `Smart_ZYNQ_SL_IO_length_20230704` correspond to?**
> A: Not explicitly answered in the thread; refer to the schematic version notes or contact the vendor.

**Q: The board is not connecting in Vivado Hardware Manager — it shows "unconnected".**
> A: The Smart ZYNQ SL does **not** have an onboard programmer. The Type-C ports are for serial communication and power only. You must use an **external Xilinx JTAG programmer** with a 2.54 mm pitch 5×2 PIN header connector.

**Q: Can BANK33 be adjusted to 1.2 V?**
> A: Technically yes (the XC7Z020 HR/HP banks support down to 1.2 V), but signal quality through the headers at 1.2 V may not be ideal in practice.

---

*Document compiled and translated from Chinese. Original source: [hellofpga.com](http://www.hellofpga.com/index.php/2023/05/10/smart-zynq-sl/)*
