#!/bin/sh
set -eu

PROFILE_ENABLED="${PROFILE_ENABLED:-false}"
SHARDS="${SHARDS:-}"

cat >/tmp/config.alloy <<'EOF'
pyroscope.write "local" {
  endpoint {
    url = "http://pyroscope:4040"
  }
}
EOF

if [ "$PROFILE_ENABLED" = "true" ]; then
  {
    echo 'pyroscope.scrape "bench" {'
    echo '  targets = ['
    echo '    {"__address__" = "vtgate:15001", "service_name" = "vtgate"},'
    for pair in $SHARDS; do
      tablet_id="${pair##*:}"
      echo "    {\"__address__\" = \"tablet-$tablet_id:15100\", \"service_name\" = \"tablet-$tablet_id\"},"
    done
    echo '  ]'
    echo '  scrape_interval = "5s"'
    echo '  delta_profiling_duration = "4s"'
    echo '  forward_to = [pyroscope.write.local.receiver]'
    echo '}'
  } >>/tmp/config.alloy
fi

exec alloy run /tmp/config.alloy
