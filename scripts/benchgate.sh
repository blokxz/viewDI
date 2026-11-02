#!/usr/bin/env bash
# Fails when a benchmark got more than THRESHOLD percent slower (sec/op) with
# statistical significance, reading `benchstat old new` output on stdin.
# benchstat prints "~" instead of a delta when the change is not significant,
# so noise does not trip the gate. The geomean row, the B/op and the
# allocs/op tables are reported but not gated.
set -euo pipefail
threshold="${1:-15}"
awk -v limit="$threshold" '
  /sec\/op/                    { gate = 1; next }
  /B\/op|allocs\/op|[^ ]\/s +│/ { gate = 0 }
  gate && $1 != "geomean" && match($0, /\+[0-9]+(\.[0-9]+)?% \(p=/) {
    pct = substr($0, RSTART + 1, RLENGTH - 1) + 0
    if (pct > limit) { printf "regression: %s\n", $0; bad = 1 }
  }
  END { exit bad }
' || { echo "benchmark gate: a sec/op regression above ${threshold}% (see above)" >&2; exit 1; }
