#!/bin/bash
set -euo pipefail

TABLET_ID="${TABLET_ID:?TABLET_ID must be set}"
SHARD="${SHARD:?SHARD must be set}"
VTTABLET_EXTRA_FLAGS="${VTTABLET_EXTRA_FLAGS:-}"

TABLET_ALIAS="local-$(printf '%010d' "$TABLET_ID")"
SOCKET="/vt/vtdataroot/vt_$(printf '%010d' "$TABLET_ID")/mysql.sock"

echo "Starting vttablet $TABLET_ALIAS for shard $SHARD..."

# shellcheck disable=SC2086
exec vttablet \
  --topo-implementation=etcd2 \
  --topo-global-server-address=etcd:2379 \
  --topo-global-root=/vitess/main \
  --tablet-path="$TABLET_ALIAS" \
  --init-keyspace=main \
  --init-shard="$SHARD" \
  --init-tablet-type=replica \
  --port=15100 \
  --grpc-port=16100 \
  --service-map='grpc-queryservice,grpc-tabletmanager,grpc-updatestream' \
  --db-socket="$SOCKET" \
  --queryserver-config-pool-size=500 \
  --queryserver-config-transaction-cap=2000 \
  --queryserver-config-query-timeout=300s \
  --pprof-http \
  --log-format=text \
  $VTTABLET_EXTRA_FLAGS
