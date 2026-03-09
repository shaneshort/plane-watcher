# This file is intentionally left as documentation only.
#
# Vivado does not support general Tcl control flow (`if`, `proc`, custom proc
# calls) inside XDC files. The old implementation-only CDC exception logic in
# this file was therefore being ignored and emitting CRITICAL WARNINGs.
#
# For the stock-tap flow:
# - the rx clock period override is now handled by rewriting the copied vendor
#   `system_constr.xdc` in `build_stock_tap.tcl`
# - the debug/CDC false-path exceptions are applied from
#   `plane_watcher_post_impl_overrides.tcl` via the implementation run's Tcl
#   pre-hook, where full Tcl is supported
