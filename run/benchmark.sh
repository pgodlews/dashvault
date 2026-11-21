#!/bin/bash -eu
# run/benchmark.sh — microSD write-speed benchmark.
#
# Writes a 160 MB test file (4 clips × 40 MB, matching one minute of Tesla
# recording) to the mutable partition (on the SD card) and measures sustained
# sequential write throughput with a 60-second timeout.
#
# Output: single JSON line on stdout.
# Appends to RESULTS_FILE (keeps last 3 results).
#
# Usage:
#   benchmark.sh          — run and store result
#   benchmark.sh --check  — print last stored result and exit (no test run)

set -o pipefail

RESULTS_FILE="${BENCHMARK_RESULTS_FILE:-/mutable/sdcard_benchmark.json}"
TEST_PREFIX="${BENCHMARK_TEST_FILE:-/mutable/teslausb_benchmark_test}"
SIZE_MB=160
BLOCK_SIZE=4    # MB — matches typical cluster size / erase block
COUNT=$(( SIZE_MB / BLOCK_SIZE ))
TIMEOUT_SECS=60

# Thresholds (MB/s)
THRESHOLD_GOOD=25
THRESHOLD_MARGINAL=15

log() { echo "[benchmark] $*" >&2; }

# ── --check mode ──────────────────────────────────────────────────────────────

if [ "${1:-}" = "--check" ]; then
    if [ -f "$RESULTS_FILE" ]; then
        cat "$RESULTS_FILE"
    else
        echo '{"results":[]}'
    fi
    exit 0
fi

# Each writing invocation owns a unique scratch file. --check never allocates one.
TEST_FILE="$(mktemp "${TEST_PREFIX}.XXXXXX")"
cleanup() { rm -f -- "$TEST_FILE"; }
trap cleanup EXIT

# ── run benchmark ──────────────────────────────────────────────────────────────

log "Writing ${SIZE_MB} MB test file (timeout ${TIMEOUT_SECS}s)…"

TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
START_NS="$(date +%s%N)"

# oflag=dsync: each block is flushed to hardware before dd moves on — this
# prevents caching from inflating the result and matches real camera write patterns.
EXIT=0
timeout "$TIMEOUT_SECS" \
     dd if=/dev/zero of="$TEST_FILE" \
        bs="${BLOCK_SIZE}M" count="$COUNT" \
        oflag=dsync conv=fsync \
        2>/dev/null || EXIT=$?
if [ $EXIT -ne 0 ]; then
    if [ $EXIT -eq 124 ]; then
        log "Timed out after ${TIMEOUT_SECS}s — card is too slow."
        RESULT=$(python3 - <<EOF
import json
result = {
    "timestamp": "$TIMESTAMP",
    "write_mbps": None,
    "rating": "slow",
    "timed_out": True,
    "size_mb": $SIZE_MB,
    "elapsed_ms": ${TIMEOUT_SECS}000
}
print(json.dumps(result))
EOF
)
    else
        log "dd failed (exit $EXIT)."
        RESULT=$(python3 -c "import json; print(json.dumps({'timestamp':'$TIMESTAMP','error':'dd failed','rating':'unknown'}))")
    fi
else
    END_NS="$(date +%s%N)"
    ELAPSED_MS=$(( (END_NS - START_NS) / 1000000 ))
    BYTES=$(( SIZE_MB * 1024 * 1024 ))

    # Calculate MB/s with one decimal place
    WRITE_MBPS=$(python3 -c "
elapsed_s = $ELAPSED_MS / 1000
mbps = $BYTES / elapsed_s / (1024 * 1024)
print(round(mbps, 1))
")

    RATING=$(python3 -c "
mbps = $WRITE_MBPS
if mbps >= $THRESHOLD_GOOD:
    print('good')
elif mbps >= $THRESHOLD_MARGINAL:
    print('marginal')
else:
    print('slow')
")

    log "Result: ${WRITE_MBPS} MB/s → ${RATING}"

    RESULT=$(python3 -c "
import json
print(json.dumps({
    'timestamp': '$TIMESTAMP',
    'write_mbps': $WRITE_MBPS,
    'rating': '$RATING',
    'timed_out': False,
    'size_mb': $SIZE_MB,
    'elapsed_ms': $ELAPSED_MS,
}))
")
fi

# ── persist result (keep last 3) ──────────────────────────────────────────────

# Pass RESULT via env var so the JSON boolean 'false' isn't interpolated
# directly as Python code (where it would be an undefined identifier).
BENCHMARK_RESULT="$RESULT" BENCHMARK_RESULTS_FILE="$RESULTS_FILE" python3 - <<'PYEOF'
import json, os

results_file = os.environ["BENCHMARK_RESULTS_FILE"]
new_result   = json.loads(os.environ["BENCHMARK_RESULT"])

# Load existing
existing = []
if os.path.exists(results_file):
    try:
        data = json.loads(open(results_file).read())
        existing = data.get("results", [])
    except (json.JSONDecodeError, KeyError):
        existing = []

# Prepend new result, keep last 3
results = ([new_result] + existing)[:3]

with open(results_file, "w") as f:
    json.dump({"results": results}, f, indent=2)

print(json.dumps(new_result))
PYEOF
