#!/bin/bash
set -euo pipefail

PHASE="${1:?Usage: entrypoint.sh <prepare|run|all>}"
WORKLOAD="${WORKLOAD:?WORKLOAD must be set}"
TABLES="${TABLES:?TABLES must be set}"
THREADS="${THREADS:?THREADS must be set}"
TABLE_SIZE="${TABLE_SIZE:-}"
SCALE="${SCALE:-}"
EXTRA="${EXTRA:-}"
WARMUP_TIME="${WARMUP_TIME:-20}"
RUN_TIME="${RUN_TIME:-60}"
WORKING_DIR="${WORKING_DIR:-}"
HOST="${HOST:-vtgate}"
PORT="${PORT:-13306}"
DB="${DB:-main}"
RUN_SUBDIR="${RUN_SUBDIR:-}"

RUN_DIR=""
BENCHMARK_STARTED_FILE=""
BENCHMARK_DONE_FILE=""
if [ -n "$RUN_SUBDIR" ]; then
  RUN_DIR="/vt/runs/$RUN_SUBDIR"
  BENCHMARK_STARTED_FILE="$RUN_DIR/.benchmark-started"
  BENCHMARK_DONE_FILE="$RUN_DIR/.benchmark-done"
fi

COMMON_ARGS=(
  --mysql-host="$HOST"
  --mysql-port="$PORT"
  --mysql-user=root
  --mysql-db="$DB"
  --db-driver=mysql
  --db-ps-mode=disable
  --rand-type=uniform
  --rand-seed=1
  --tables="$TABLES"
  --threads="$THREADS"
)

if [ -n "$TABLE_SIZE" ]; then
  COMMON_ARGS+=(--table-size="$TABLE_SIZE")
fi
if [ -n "$SCALE" ]; then
  COMMON_ARGS+=(--scale="$SCALE")
fi

run_prepare() {
  echo "=== Preparing data ==="
  if [ -n "$WORKING_DIR" ]; then
    cd "$WORKING_DIR"
  fi
  # shellcheck disable=SC2086
  sysbench "$WORKLOAD" "${COMMON_ARGS[@]}" $EXTRA prepare
}

run_benchmark() {
  echo "=== Running benchmark ($RUN_TIME seconds, warmup $WARMUP_TIME seconds) ===" >&2
  if [ -n "$WORKING_DIR" ]; then
    cd "$WORKING_DIR"
  fi
  if [ -n "$RUN_DIR" ]; then
    mkdir -p "$RUN_DIR"
    rm -f "$BENCHMARK_STARTED_FILE" "$BENCHMARK_DONE_FILE"
    date +%s%3N > "$BENCHMARK_STARTED_FILE"
    trap 'date +%s%3N > "$BENCHMARK_DONE_FILE"' RETURN
  fi
  # shellcheck disable=SC2086
  sysbench "$WORKLOAD" "${COMMON_ARGS[@]}" $EXTRA \
    --time="$RUN_TIME" \
    --warmup-time="$WARMUP_TIME" \
    --report-interval=5 \
    run
}

case "$PHASE" in
  prepare)
    run_prepare
    ;;
  run)
    run_benchmark
    ;;
  all)
    run_prepare
    run_benchmark
    ;;
  *)
    echo "Unknown phase: $PHASE"
    echo "Usage: entrypoint.sh <prepare|run|all>"
    exit 1
    ;;
esac
