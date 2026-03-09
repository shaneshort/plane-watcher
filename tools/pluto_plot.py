#!/usr/bin/env python3
"""Capture IQ from a PlutoSDR and generate diagnostic plots.

Examples:
    uv run --with pyadi-iio --with matplotlib python tools/pluto_plot.py
    uv run --with pyadi-iio --with matplotlib python tools/pluto_plot.py --uri ip:pluto.local --output /tmp/pluto.png
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys

import numpy as np


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Capture and plot PlutoSDR RX data")
    ap.add_argument("--uri", default="ip:pluto.local", help="IIO URI for Pluto")
    ap.add_argument("--rx-lo", type=float, default=1_090_000_000, help="RX LO in Hz")
    ap.add_argument("--sample-rate", type=float, default=10_000_000, help="Sample rate in Hz")
    ap.add_argument("--bandwidth", type=float, default=2_000_000, help="RF bandwidth in Hz")
    ap.add_argument(
        "--gain-mode",
        default="slow_attack",
        choices=["manual", "slow_attack", "fast_attack"],
        help="AD936x gain control mode",
    )
    ap.add_argument("--gain-db", type=float, default=60.0, help="Manual RX hardware gain in dB")
    ap.add_argument("--buffer-size", type=int, default=262144, help="Number of complex samples to capture")
    ap.add_argument("--avg", type=int, default=8, help="Number of FFT frames to average")
    ap.add_argument("--time-samples", type=int, default=20000, help="Samples to show in the overview time plot")
    ap.add_argument("--burst-samples", type=int, default=512, help="Samples to show around the strongest burst")
    ap.add_argument("--channel", type=int, default=0, help="RX channel index")
    ap.add_argument("--output", default="/tmp/pluto_plot.png", help="Write plot to file")
    ap.add_argument("--show", action="store_true", help="Open an interactive matplotlib window instead of saving")
    return ap.parse_args()


def dbfs(x: np.ndarray) -> np.ndarray:
    return 20.0 * np.log10(np.maximum(np.abs(x), 1e-12))


def moving_average(x: np.ndarray, width: int) -> np.ndarray:
    width = max(1, int(width))
    kernel = np.ones(width, dtype=np.float64) / width
    return np.convolve(x, kernel, mode="same")


def print_context_devices(uri: str) -> None:
    try:
        import iio
    except ImportError:
        print("libiio Python bindings not available for device listing", file=sys.stderr)
        return

    try:
        ctx = iio.Context(uri)
    except Exception as exc:
        print(f"failed to open IIO context at {uri}: {exc}", file=sys.stderr)
        print("check network reachability to the Pluto first", file=sys.stderr)
        return

    names = []
    for dev in ctx.devices:
        names.append(dev.name or "(unnamed)")

    print(f"IIO devices at {uri}:", file=sys.stderr)
    for name in names:
        print(f"  - {name}", file=sys.stderr)

    if "cf-ad9361-lpc" not in names:
        print("", file=sys.stderr)
        print("expected RX streaming device 'cf-ad9361-lpc' is missing", file=sys.stderr)
        print("this usually means the Pluto is not in a stock RX-capture-capable firmware/DT state", file=sys.stderr)
        print("or the current image omitted the ADI RX DMA chain", file=sys.stderr)


def main() -> int:
    args = parse_args()

    try:
        import matplotlib.pyplot as plt
        import adi
    except ImportError as exc:
        print(
            "missing Python package:",
            exc,
            file=sys.stderr,
        )
        print(
            "run with: uv run --with pyadi-iio --with matplotlib python tools/pluto_plot.py",
            file=sys.stderr,
        )
        return 2

    try:
        try:
            sdr = adi.Pluto(args.uri)
        except Exception:
            # Fallback for older pyadi-iio APIs.
            sdr = adi.ad9361(uri=args.uri)
    except Exception as exc:
        print(f"failed to open Pluto RX stream at {args.uri}: {exc}", file=sys.stderr)
        print_context_devices(args.uri)
        return 1

    sdr.sample_rate = int(args.sample_rate)
    sdr.rx_rf_bandwidth = int(args.bandwidth)
    sdr.rx_lo = int(args.rx_lo)
    sdr.rx_buffer_size = int(args.buffer_size)
    sdr.rx_enabled_channels = [int(args.channel)]
    sdr.gain_control_mode_chan0 = args.gain_mode
    if args.gain_mode == "manual":
        sdr.rx_hardwaregain_chan0 = float(args.gain_db)

    frames: list[np.ndarray] = []
    for _ in range(max(args.avg, 1)):
        data = sdr.rx()
        if isinstance(data, list):
            data = data[0]
        frames.append(np.asarray(data, dtype=np.complex128))

    iq = frames[-1]
    n = len(iq)
    if n == 0:
        print("capture returned no samples", file=sys.stderr)
        return 1

    i = iq.real.astype(np.float64)
    q = iq.imag.astype(np.float64)
    amp = np.abs(iq)
    power = amp ** 2
    power_db = 10.0 * np.log10(np.maximum(power, 1e-12))
    max_abs = float(np.max(np.abs(np.concatenate((i, q)))))
    clip_ratio = float(np.mean((np.abs(i) >= 2047) | (np.abs(q) >= 2047)))
    rms = float(np.sqrt(np.mean(power)))
    crest_db = 20.0 * np.log10(max(rms and amp.max() / rms, 1e-12))
    dc_i = float(np.mean(i))
    dc_q = float(np.mean(q))

    window = np.hanning(n)
    spec_accum = np.zeros(n, dtype=np.float64)
    for frame in frames:
        fft = np.fft.fftshift(np.fft.fft(frame * window))
        spec_accum += np.abs(fft)
    spec = dbfs(spec_accum / len(frames))
    freqs = np.fft.fftshift(np.fft.fftfreq(n, d=1.0 / args.sample_rate))
    time_count = min(args.time_samples, n)
    t = np.arange(time_count) / args.sample_rate * 1e3
    overview_power = power[:time_count]
    smooth_width = max(8, int(args.sample_rate / 1_000_000))
    overview_smooth = moving_average(overview_power, smooth_width)

    burst_idx = int(np.argmax(power))
    burst_half = max(8, args.burst_samples // 2)
    burst_start = max(0, burst_idx - burst_half)
    burst_end = min(n, burst_idx + burst_half)
    burst_iq = iq[burst_start:burst_end]
    burst_t_us = (np.arange(len(burst_iq)) + burst_start - burst_idx) / args.sample_rate * 1e6
    burst_power = np.abs(burst_iq) ** 2
    burst_i = burst_iq.real
    burst_q = burst_iq.imag

    spec_peak_idx = int(np.argmax(spec))
    spec_peak_mhz = float(freqs[spec_peak_idx] / 1e6)
    spec_peak_db = float(spec[spec_peak_idx])

    fig = plt.figure(figsize=(15, 11))
    gs = fig.add_gridspec(3, 2, height_ratios=[1.0, 1.0, 1.2])
    ax0 = fig.add_subplot(gs[0, 0])
    ax1 = fig.add_subplot(gs[0, 1])
    ax2 = fig.add_subplot(gs[1, 0])
    ax3 = fig.add_subplot(gs[1, 1])
    ax4 = fig.add_subplot(gs[2, 0])
    ax5 = fig.add_subplot(gs[2, 1])

    fig.suptitle(f"Pluto RX Diagnostics @ {args.rx_lo/1e6:.3f} MHz, {args.sample_rate/1e6:.3f} MSPS, URI {args.uri}")

    ax0.plot(t, overview_power, linewidth=0.6, alpha=0.5, label="Instantaneous")
    ax0.plot(t, overview_smooth[:time_count], linewidth=1.1, label="Smoothed")
    ax0.set_title("Time-Domain Power Overview")
    ax0.set_xlabel("Time (ms)")
    ax0.set_ylabel("Power")
    ax0.grid(True, alpha=0.3)
    ax0.legend(loc="upper right")

    ax1.plot(freqs / 1e6, spec, linewidth=0.8)
    ax1.set_title("Averaged Spectrum")
    ax1.set_xlabel("Baseband Frequency (MHz)")
    ax1.set_ylabel("Relative Magnitude (dB)")
    ax1.grid(True, alpha=0.3)
    ax1.axvline(spec_peak_mhz, color="tab:red", alpha=0.5, linewidth=1.0)
    ax1.text(
        0.02,
        0.98,
        f"Peak {spec_peak_mhz:+.3f} MHz\n{spec_peak_db:.1f} dB",
        transform=ax1.transAxes,
        va="top",
        ha="left",
        bbox={"facecolor": "white", "alpha": 0.8, "edgecolor": "none"},
    )

    ax2.specgram(
        iq,
        NFFT=1024,
        Fs=args.sample_rate,
        noverlap=768,
        xextent=(0, n / args.sample_rate * 1e3),
        cmap="viridis",
    )
    ax2.set_title("Spectrogram")
    ax2.set_xlabel("Time (ms)")
    ax2.set_ylabel("Baseband Frequency (Hz)")

    ax3.plot(burst_t_us, burst_i, linewidth=0.9, label="I")
    ax3.plot(burst_t_us, burst_q, linewidth=0.9, label="Q")
    ax3.plot(burst_t_us, burst_power / max(np.max(burst_power), 1e-12) * max(np.max(np.abs(burst_i)), np.max(np.abs(burst_q)), 1.0),
             linewidth=0.9, alpha=0.7, label="Power (scaled)")
    ax3.set_title("Strongest Burst Zoom")
    ax3.set_xlabel("Time relative to burst peak (us)")
    ax3.set_ylabel("Amplitude")
    ax3.grid(True, alpha=0.3)
    ax3.legend(loc="upper right")

    ax4.hist(i, bins=120, alpha=0.6, label="I")
    ax4.hist(q, bins=120, alpha=0.6, label="Q")
    ax4.set_title("I/Q Histogram")
    ax4.set_xlabel("ADC Code")
    ax4.set_ylabel("Count")
    ax4.grid(True, alpha=0.3)
    ax4.legend(loc="upper right")

    ax5.axis("off")
    summary = [
        f"Samples: {n}",
        f"Capture length: {n / args.sample_rate * 1e3:.2f} ms",
        f"Gain mode: {args.gain_mode}" + (f" ({args.gain_db:.1f} dB)" if args.gain_mode == "manual" else ""),
        f"RMS amplitude: {rms:.1f}",
        f"Peak amplitude: {amp.max():.1f}",
        f"Crest factor: {crest_db:.1f} dB",
        f"Clipping ratio: {clip_ratio * 100:.3f}%",
        f"Max |I/Q| code: {max_abs:.1f}",
        f"DC offset: I={dc_i:.2f}, Q={dc_q:.2f}",
        f"Median power: {np.median(power):.1f}",
        f"99.9th pct power: {np.percentile(power, 99.9):.1f}",
        f"Peak spectral bin: {spec_peak_mhz:+.3f} MHz",
        f"Burst peak time: {burst_idx / args.sample_rate * 1e3:.3f} ms",
        f"Burst peak power: {power[burst_idx]:.1f}",
    ]
    ax5.text(
        0.02,
        0.98,
        "\n".join(summary),
        va="top",
        ha="left",
        family="monospace",
        fontsize=10,
        bbox={"facecolor": "#f4f4f4", "alpha": 0.9, "edgecolor": "#d0d0d0"},
    )

    fig.tight_layout(rect=(0, 0, 1, 0.965))

    if args.show:
        plt.show()
    else:
        fig.savefig(args.output, dpi=150)
        print(f"wrote {args.output}")
        try:
            subprocess.Popen(
                ["xdg-open", args.output],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                start_new_session=True,
                env={**os.environ, "MPLBACKEND": "Agg"},
            )
            print("opened image viewer in background")
        except Exception as exc:
            print(f"could not launch image viewer: {exc}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
