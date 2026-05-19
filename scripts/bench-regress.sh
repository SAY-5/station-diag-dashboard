#!/usr/bin/env bash
# bench-regress.sh compares the two most recent bench reports under
# bench/results and fails if any metric drifted past the allowed threshold:
# throughput dropping more than DRIFT percent, or a P99 latency growing more
# than DRIFT percent. With fewer than two reports it records a baseline and
# passes, so the first run on a fresh checkout is never a failure.
set -euo pipefail

RESULTS_DIR="${RESULTS_DIR:-bench/results}"
DRIFT="${DRIFT:-30}"

# Collect reports portably: mapfile is bash 4+, but macOS ships bash 3.2.
reports=()
while IFS= read -r line; do
  reports+=("$line")
done < <(ls -1 "${RESULTS_DIR}"/*.json 2>/dev/null | sort)

if [ "${#reports[@]}" -lt 2 ]; then
  echo "bench-regress: fewer than two reports in ${RESULTS_DIR}, baseline accepted"
  exit 0
fi

baseline="${reports[$((${#reports[@]} - 2))]}"
current="${reports[$((${#reports[@]} - 1))]}"
echo "bench-regress: baseline=${baseline}"
echo "bench-regress: current =${current}"

# The comparison is delegated to a small awk-free Go-free jq pipeline. jq is
# preinstalled on every GitHub runner; locally install it if missing.
if ! command -v jq >/dev/null 2>&1; then
  echo "bench-regress: jq is required" >&2
  exit 2
fi

fail=0
for k in 1 10 50; do
  for metric in events_per_sec rule_eval_p99_us fanout_p99_us; do
    base=$(jq -r --argjson k "$k" --arg m "$metric" \
      '.results[] | select(.subscribers==$k) | .[$m]' "$baseline")
    cur=$(jq -r --argjson k "$k" --arg m "$metric" \
      '.results[] | select(.subscribers==$k) | .[$m]' "$current")
    if [ -z "$base" ] || [ -z "$cur" ] || [ "$base" = "null" ] || [ "$cur" = "null" ]; then
      continue
    fi
    # higher_is_better is true only for throughput.
    higher=0
    [ "$metric" = "events_per_sec" ] && higher=1
    # Latency metrics carry a noise floor: shared CI runners jitter
    # sub-millisecond P99s wildly, so a percentage gate on a tiny absolute
    # value is meaningless. A latency drift only counts when the current
    # value also clears NOISE_FLOOR_US microseconds.
    floor="${NOISE_FLOOR_US:-200}"
    verdict=$(awk -v base="$base" -v cur="$cur" -v drift="$DRIFT" \
      -v hib="$higher" -v floor="$floor" 'BEGIN {
      if (base <= 0) { print "skip 0"; exit }
      if (hib == 1) {
        delta = (base - cur) / base * 100
        printf "%s %.1f", (delta > drift ? "FAIL" : "ok"), delta
      } else {
        delta = (cur - base) / base * 100
        if (cur < floor) { printf "ok %.1f", delta }
        else             { printf "%s %.1f", (delta > drift ? "FAIL" : "ok"), delta }
      }
    }')
    status=${verdict%% *}
    pct=${verdict##* }
    printf "  K=%-3d %-20s base=%-14s cur=%-14s drift=%6s%%  %s\n" \
      "$k" "$metric" "$base" "$cur" "$pct" "$status"
    [ "$status" = "FAIL" ] && fail=1
  done
done

if [ "$fail" -ne 0 ]; then
  echo "bench-regress: regression detected (threshold ${DRIFT}%)" >&2
  exit 1
fi
echo "bench-regress: within ${DRIFT}% threshold"
