#!/bin/bash
set -euo pipefail

VTGATE_WORKLOAD_MODE="${VTGATE_WORKLOAD_MODE:-}"
VTGATE_MAX_MEMORY_ROWS="${VTGATE_MAX_MEMORY_ROWS:-}"

EXTRA_FLAGS=""
if [ -n "$VTGATE_WORKLOAD_MODE" ]; then
  EXTRA_FLAGS="$EXTRA_FLAGS --mysql-default-workload=$VTGATE_WORKLOAD_MODE"
fi
if [ -n "$VTGATE_MAX_MEMORY_ROWS" ]; then
  EXTRA_FLAGS="$EXTRA_FLAGS --max-memory-rows=$VTGATE_MAX_MEMORY_ROWS"
fi

echo "Starting vtgate..."
# shellcheck disable=SC2086
exec vtgate \
  --topo-implementation=etcd2 \
  --topo-global-server-address=etcd:2379 \
  --topo-global-root=/vitess/main \
  --cell=local \
  --cells-to-watch=local \
  --mysql-server-port=13306 \
  --grpc-port=15306 \
  --port=15001 \
  --mysql-auth-server-impl=none \
  --planner-version=gen4 \
  --tablet-types-to-wait=PRIMARY,REPLICA \
  --enable-buffer \
  --buffer-size=300 \
  --pprof-http \
  --service-map='grpc-vtgateservice' \
  --log-format=text \
  $EXTRA_FLAGS
