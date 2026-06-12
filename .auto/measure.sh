#!/bin/bash
set -euo pipefail

# Measure Vitess oltp benchmark QPS with full profiling enabled.
# Runs the sysbench run-phase 3 times inside one cluster (prepare once) and
# reports the MEDIAN to beat the ±4-5% single-run noise floor.
# Outputs METRIC lines parsed by the autoresearch loop, plus the run dir
# so the agent can analyze the exported pprof profiles.

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Fast pre-check: make sure the Go code compiles before paying for a docker build.
go build ./go/cmd/vtgate ./go/cmd/vttablet 1>&2

TS="$(date +%s)"
NAME="auto-$TS"
REPEAT=3

BENCH_REPEAT=$REPEAT make bench BENCH=oltp PROFILE=1 NAME="$NAME" 1>&2

RUN_DIR="$(find benchmarks/runs/oltp -maxdepth 1 -type d -name "*-$NAME" | tail -1)"

if [ ! -f "$RUN_DIR/vitess.json" ]; then
  echo "ERROR: no vitess.json in $RUN_DIR" >&2
  exit 1
fi

# Collect per-run samples (vitess.json, vitess2.json, vitess3.json, ...).
qps_samples=()
tps_samples=()
p95_samples=()
for f in "$RUN_DIR/vitess.json" "$RUN_DIR"/vitess[0-9]*.json; do
  [ -f "$f" ] || continue
  qps_samples+=("$(jq -r '.qps.total' "$f")")
  tps_samples+=("$(jq -r '.tps' "$f")")
  p95_samples+=("$(jq -r '.latency' "$f")")
done

median() {
  printf '%s\n' "$@" | sort -n | awk '{a[NR]=$1} END {print a[int((NR+1)/2)]}'
}

echo "RUN_DIR $RUN_DIR"
echo "SAMPLES qps=${qps_samples[*]} tps=${tps_samples[*]} p95=${p95_samples[*]}"
echo "METRIC qps=$(median "${qps_samples[@]}")"
echo "METRIC tps=$(median "${tps_samples[@]}")"
echo "METRIC p95_ms=$(median "${p95_samples[@]}")"
