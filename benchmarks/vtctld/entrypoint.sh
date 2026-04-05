#!/bin/bash
set -euo pipefail

echo "Adding cell info..."
vtctl \
  --topo-implementation=etcd2 \
  --topo-global-server-address=etcd:2379 \
  --topo-global-root=/vitess/main \
  AddCellInfo \
  --root=/vitess/main/local \
  --server_address=etcd:2379 \
  local || true

echo "Starting vtctld..."
exec vtctld \
  --topo-implementation=etcd2 \
  --topo-global-server-address=etcd:2379 \
  --topo-global-root=/vitess/main \
  --cell=local \
  --port=15000 \
  --grpc-port=15999 \
  --service-map='grpc-vtctl,grpc-vtctld' \
  --log-format=text
