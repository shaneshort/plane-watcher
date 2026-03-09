#!/bin/bash
# =============================================================================
# detector_sweep.sh — Combined knob sweep with optional dual capture comparison
# =============================================================================
#
# Wraps knob_sweep.py and optionally runs dual_capture.sh + capture_compare.py
# at each knob setting.
#
# Usage:
#   # Stats-only sweep (fast, ~35s per setting):
#   ./tools/detector_sweep.sh --holdoff 128,512,1024,2240
#
#   # With capture comparison (~50s per setting):
#   ./tools/detector_sweep.sh --holdoff 128,512,1024,2240 --with-capture
#
#   # Custom dwell and capture duration:
#   ./tools/detector_sweep.sh --holdoff 512,1024 --dwell 60 --with-capture --capture-seconds 10
#
#   # Multiple knobs:
#   ./tools/detector_sweep.sh --holdoff 512,1024,2240 --quiet-score-shift 1,2,3
#
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

# Defaults.
HOST="pluto.local:8080"
DWELL=30
SETTLE=5
GAIN=24
CAPTURE_SECONDS=10
WITH_CAPTURE=false

# Knob sweep args (passed through to knob_sweep.py).
SWEEP_ARGS=()

# Parse arguments.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --host)
            HOST="$2"; shift 2 ;;
        --dwell)
            DWELL="$2"; shift 2 ;;
        --settle)
            SETTLE="$2"; shift 2 ;;
        --gain)
            GAIN="$2"; shift 2 ;;
        --capture-seconds)
            CAPTURE_SECONDS="$2"; shift 2 ;;
        --with-capture)
            WITH_CAPTURE=true; shift ;;
        --holdoff|--quiet-score-shift|--snr-ratio-shift)
            SWEEP_ARGS+=("$1" "$2"); shift 2 ;;
        -h|--help)
            echo "Usage: $0 [--holdoff V1,V2,...] [--quiet-score-shift V1,...] [--snr-ratio-shift V1,...]"
            echo "       [--dwell SECONDS] [--settle SECONDS] [--gain DB]"
            echo "       [--with-capture] [--capture-seconds SECONDS]"
            echo "       [--host HOST:PORT]"
            echo ""
            echo "Stats-only sweep by default. Add --with-capture for dump1090 comparison."
            exit 0 ;;
        *)
            echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
done

if [[ ${#SWEEP_ARGS[@]} -eq 0 ]]; then
    echo "No knobs specified. Use --holdoff, --quiet-score-shift, or --snr-ratio-shift." >&2
    exit 1
fi

# Create output directory.
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTDIR="/tmp/detector_sweep_${TIMESTAMP}"
mkdir -p "$OUTDIR"

echo "=== Detector Sweep ==="
echo "  Host:     $HOST"
echo "  Dwell:    ${DWELL}s (settle: ${SETTLE}s)"
echo "  Knobs:    ${SWEEP_ARGS[*]}"
echo "  Capture:  $WITH_CAPTURE"
echo "  Output:   $OUTDIR"
echo ""

if [[ "$WITH_CAPTURE" == true ]]; then
    # -------------------------------------------------------------------------
    # With-capture mode: manually iterate knob values, capture at each.
    # -------------------------------------------------------------------------
    echo "=== Capture mode: sweeping with dual capture + dump1090 comparison ==="
    echo ""

    # Extract the pluto host from the feeder host (strip port).
    PLUTO_HOST="${HOST%%:*}"

    # Parse sweep args into knob/values pairs.
    KNOB_SETS=()
    i=0
    while [[ $i -lt ${#SWEEP_ARGS[@]} ]]; do
        knob_flag="${SWEEP_ARGS[$i]}"
        knob_values="${SWEEP_ARGS[$((i + 1))]}"
        # Convert flag to API knob name.
        case "$knob_flag" in
            --holdoff) knob_name="holdoff" ;;
            --quiet-score-shift) knob_name="quiet-score-shift" ;;
            --snr-ratio-shift) knob_name="snr-ratio-shift" ;;
        esac
        KNOB_SETS+=("$knob_name:$knob_values")
        i=$((i + 2))
    done

    STEP=0
    for knob_spec in "${KNOB_SETS[@]}"; do
        knob_name="${knob_spec%%:*}"
        knob_values="${knob_spec##*:}"
        api_path="/api/detector/${knob_name}"

        IFS=',' read -ra VALUES <<< "$knob_values"
        for val in "${VALUES[@]}"; do
            STEP=$((STEP + 1))
            STEP_DIR="${OUTDIR}/step_${STEP}_${knob_name}_${val}"
            mkdir -p "$STEP_DIR"

            echo "--- Step ${STEP}: ${knob_name}=${val} ---"

            # Set the knob.
            curl -s -X POST "http://${HOST}${api_path}" \
                -H 'Content-Type: application/json' \
                -d "{\"value\": ${val}}" > /dev/null

            # Settle.
            echo "  Settling (${SETTLE}s)..."
            sleep "$SETTLE"

            # Capture stats before.
            curl -s "http://${HOST}/api/stats" > "${STEP_DIR}/stats_before.json"

            # Dual capture.
            echo "  Capturing ${CAPTURE_SECONDS}s (beast + IQ)..."
            BEAST_FILE="${STEP_DIR}/beast.bin"
            IQ_FILE="${STEP_DIR}/iq.raw"

            timeout "$((CAPTURE_SECONDS + 2))" nc "${PLUTO_HOST}" 30005 > "$BEAST_FILE" &
            NC_PID=$!
            sleep 0.3

            uv run --with pyadi-iio python "${SCRIPT_DIR}/pluto_capture.py" \
                --uri "ip:${PLUTO_HOST}" \
                --gain-mode manual --gain-db "$GAIN" \
                --seconds "$CAPTURE_SECONDS" \
                --output "$IQ_FILE" \
                --stats-out "${STEP_DIR}/iq_stats.json" 2>&1 | tail -3

            wait "$NC_PID" 2>/dev/null || true

            # Capture stats after.
            curl -s "http://${HOST}/api/stats" > "${STEP_DIR}/stats_after.json"
            curl -s "http://${HOST}/api/drops" > "${STEP_DIR}/drops.json"

            # Compare.
            echo "  Comparing FPGA vs dump1090..."
            uv run python "${SCRIPT_DIR}/capture_compare.py" \
                --beast "$BEAST_FILE" \
                --iq "$IQ_FILE" \
                --json > "${STEP_DIR}/compare.json" 2>"${STEP_DIR}/compare.log"

            # Extract summary.
            FPGA_TOTAL=$(python3 -c "import json; d=json.load(open('${STEP_DIR}/compare.json')); print(d['fpga_total'])")
            D1090_TOTAL=$(python3 -c "import json; d=json.load(open('${STEP_DIR}/compare.json')); print(d['d1090_total'])")
            MATCHED=$(python3 -c "import json; d=json.load(open('${STEP_DIR}/compare.json')); print(d['matched'])")

            # Stats delta.
            MSG_BEFORE=$(python3 -c "import json; print(json.load(open('${STEP_DIR}/stats_before.json')).get('msg_count', 0))")
            MSG_AFTER=$(python3 -c "import json; print(json.load(open('${STEP_DIR}/stats_after.json')).get('msg_count', 0))")
            MSG_DELTA=$((MSG_AFTER - MSG_BEFORE))

            echo "  ${knob_name}=${val}: fpga=${FPGA_TOTAL} d1090=${D1090_TOTAL} matched=${MATCHED} msg_delta=${MSG_DELTA}"
            echo "${knob_name},${val},${FPGA_TOTAL},${D1090_TOTAL},${MATCHED},${MSG_DELTA}" >> "${OUTDIR}/summary.csv"
            echo ""
        done
    done

    # Print final summary.
    echo ""
    echo "=== Capture Sweep Summary ==="
    printf "%-22s %6s %6s %8s %7s %9s\n" "Knob" "Value" "FPGA" "dump1090" "Match" "msg_delta"
    echo "--------------------------------------------------------------"
    while IFS=',' read -r knob val fpga d1090 match msg_d; do
        printf "%-22s %6s %6s %8s %7s %9s\n" "$knob" "$val" "$fpga" "$d1090" "$match" "$msg_d"
    done < "${OUTDIR}/summary.csv"

    echo ""
    echo "Detailed results in: ${OUTDIR}/"

else
    # -------------------------------------------------------------------------
    # Stats-only mode: delegate entirely to knob_sweep.py.
    # -------------------------------------------------------------------------
    uv run python "${SCRIPT_DIR}/knob_sweep.py" \
        --host "$HOST" \
        --dwell "$DWELL" \
        --settle "$SETTLE" \
        --output "${OUTDIR}/sweep.csv" \
        "${SWEEP_ARGS[@]}"

    echo ""
    echo "Results in: ${OUTDIR}/"
fi
