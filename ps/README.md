# PS Tooling

`ps/` is the Linux userspace side of the receiver. It is a standalone Go
module, but it is still coupled to this repository's FPGA register contract,
radio assumptions, and operational workflow.

The layout now follows the useful parts of the common Go project-layout
pattern:

- `cmd/` contains entrypoints
- `internal/` contains private application packages that are not intended as a public library
- `bin/<target>/` is the build output location for local and cross-built binaries

## Main Runtime

- `cmd/plane-feeder`: production daemon
  - reads decoded frames from the AXI register block via `/dev/mem`
  - pushes Beast output on TCP port `30005`
  - serves the embedded dashboard and JSON API on port `8080`
  - applies radio and detector defaults at startup

## Support Commands

- `cmd/regdump`: inspect registers, counters, and radio state
- `cmd/regpeek`: dump raw register words
- `cmd/fifo-monitor`: watch FIFO and overflow state continuously
- `cmd/pps-check`: validate PPS cadence
- `cmd/replay`: replay captured/reference frames without hardware
- `cmd/beast-client`: inspect Beast output from a feeder/replay server
- `cmd/collect-stats`: sample `/api/stats?debug=1` into CSV
- `cmd/sweep-gain`: sweep manual gain values over the HTTP control API
- `cmd/tune-detector`: sweep detector settings over the HTTP control API
- `cmd/watch-stats`: terminal dashboard for live tuning metrics and trends

## Build

PS Go tools are built via `builder` (`ps/cmd/builder/`):

```sh
# Hot-swap fresh ARM binaries onto a running board:
go -C ps run ./cmd/builder --ps-hotswap

# Rebuild the Petalinux image with fresh tools slipstreamed in:
go -C ps run ./cmd/builder --ps-slipstream --petalinux --deploy=ssh --reboot

# TUI (no flags):
go -C ps run ./cmd/builder
```

Which commands cross-build for ARM vs host is policy: see
`ps/cmd/build.toml`. `builder` discovers `ps/cmd/*` directories at runtime
and applies the `targets` setting for each.

`builder` does not provide a host-batch build mode; for that, use `go build`
directly:

```sh
cd ps && go build ./cmd/<name>
# or all host-targeted commands:
cd ps && for d in cmd/*/; do go build -o /tmp/$(basename $d) ./$d; done
```

For the design, see `docs/plans/2026-05-14-builder-tui-design.md`.

Live tuning dashboard:

```sh
cd ps
go run ./cmd/watch-stats --base-url http://pluto.local:8080
```

This polls `/api/stats?debug=1` and redraws a terminal dashboard with rolling
charts for message rate, valid rate, drop rate, invalid DF rate, aircraft
count, and frontend headroom activity.

## HTTP Surface

Served by `plane-feeder`:

- `GET /api/stats`
- `GET /api/stats?debug=1`
- `GET /api/aircraft`
- `POST /api/radio/gain-mode`
- `POST /api/radio/gain`
- `POST /api/detector/quiet-score-shift`
- `POST /api/detector/snr-ratio-shift`

## Repo Boundary

`ps/` should stay in this repository for now.

Reasons:

- the Go module depends directly on the frozen AXI register map in `regs`
- the control/API surface is defined by this project's FPGA implementation
- replay, tuning, and diagnostics are part of board bring-up, not generic SDK tooling

If the register layer stabilizes across multiple hardware projects, the only
piece that would make sense to extract later is a small reusable Go library,
not the full `plane-feeder` application tree.
