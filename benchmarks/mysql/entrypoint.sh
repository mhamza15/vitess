#!/bin/bash
set -euo pipefail

TABLET_ID="${TABLET_ID:?TABLET_ID must be set}"

# Ensure tablet data directory exists
mkdir -p "/vt/vtdataroot/vt_$(printf '%010d' "$TABLET_ID")"

echo "Starting mysqlctld for tablet $TABLET_ID..."
export EXTRA_MY_CNF="/vt/config/extra_my.cnf"
exec mysqlctld \
  --tablet-uid="$TABLET_ID" \
  --mysql-port=3306 \
  --log-format=text
