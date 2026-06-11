#!/bin/bash
set -euo pipefail

# Measure Vitess oltp benchmark QPS with full profiling enabled.
# Outputs METRIC lines parsed by the autoresearch loop, plus the run dir
# so the agent can analyze the exported pprof profiles.

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Fast pre-check: make sure the Go code compiles before paying for a docker build.
go build ./go/cmd/vtgate ./go/cmd/vttablet 1>&2

TS="$(date +%s)"
NAME="auto-$TS"

make bench BENCH=oltp PROFILE=1 NAME="$NAME" 1>&2

RUN_DIR="$(find benchmarks/runs/oltp -maxdepth 1 -type d -name "*-$NAME" | tail -1)"

if [ ! -f "$RUN_DIR/vitess.json" ]; then
  echo "ERROR: no vitess.json in $RUN_DIR" >&2
  exit 1
fi

qps=$(jq -r '.qps.total' "$RUN_DIR/vitess.json")
tps=$(jq -r '.tps' "$RUN_DIR/vitess.json")
p95=$(jq -r '.latency' "$RUN_DIR/vitess.json")

echo "RUN_DIR $RUN_DIR"
echo "METRIC qps=$qps"
echo "METRIC tps=$tps"
echo "METRIC p95_ms=$p95"
