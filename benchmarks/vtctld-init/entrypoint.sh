#!/bin/bash
set -euo pipefail

SHARDS="${SHARDS:?SHARDS must be set (space-separated shard:tablet_id pairs, e.g. '-80:1001 80-:2001')}"
VSCHEMA_FILE="${VSCHEMA_FILE:?VSCHEMA_FILE must be set}"

VTCTLD_HOST="vtctld"
VTCTLD_PORT="15999"

vtctldclient() {
  command vtctldclient --server "$VTCTLD_HOST:$VTCTLD_PORT" --log-format=text "$@"
}

# Initialize shards
for pair in $SHARDS; do
  shard="${pair%%:*}"
  tablet_id="${pair##*:}"
  tablet_alias="local-$(printf '%010d' "$tablet_id")"

  echo "Initializing shard main/$shard with primary $tablet_alias..."
  vtctldclient InitShardPrimary \
    --force \
    "main/$shard" \
    "$tablet_alias"

  echo "Shard main/$shard initialized."
done

# Apply VSchema
echo "Applying VSchema from $VSCHEMA_FILE..."
vtctldclient ApplyVSchema \
  --vschema-file="/vt/vschema/$VSCHEMA_FILE" \
  main

echo "VSchema applied successfully."
echo "Cluster initialization complete."
