#!/bin/bash
set -euo pipefail

BENCHMARKS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$BENCHMARKS_DIR/benchmarks.yml"

NO_TEARDOWN=false
MYSQL_MODE=false
PROFILE=false
BENCH=""
RUN_NAME=""

while [ $# -gt 0 ]; do
  case "$1" in
    --no-teardown) NO_TEARDOWN=true ;;
    --mysql) MYSQL_MODE=true ;;
    --profile) PROFILE=true ;;
    --name) RUN_NAME="$2"; shift ;;
    --name=*) RUN_NAME="${1#--name=}" ;;
    -*) echo "Unknown flag: $1"; exit 1 ;;
    *) BENCH="$1" ;;
  esac
  shift
done

if [ -z "$BENCH" ]; then
  echo "Usage: $(basename "$0") <benchmark> [--name <name>] [--profile] [--no-teardown] [--mysql]"
  exit 1
fi

# Validate benchmark exists in config
if ! yq -e ".benchmarks[\"$BENCH\"]" "$CONFIG_FILE" &>/dev/null; then
  echo "Error: benchmark '$BENCH' not found in $CONFIG_FILE"
  echo "Available benchmarks:"
  yq -r '.benchmarks | keys[]' "$CONFIG_FILE" | sed 's/^/  /'
  exit 1
fi

# Read configuration from benchmarks.yml
sharded=$(yq -r ".benchmarks[\"$BENCH\"].vitess.sharded" "$CONFIG_FILE")
vschema=$(yq -r ".benchmarks[\"$BENCH\"].vitess.vschema" "$CONFIG_FILE")
vtgate_workload_mode=$(yq -r ".benchmarks[\"$BENCH\"].vitess.vtgate_workload_mode // \"\"" "$CONFIG_FILE")
vtgate_max_memory_rows=$(yq -r ".benchmarks[\"$BENCH\"].vitess.vtgate_max_memory_rows // \"\"" "$CONFIG_FILE")
vttablet_extra_flags=$(yq -r ".benchmarks[\"$BENCH\"].vitess.vttablet_extra_flags // \"\"" "$CONFIG_FILE")

workload=$(yq -r ".benchmarks[\"$BENCH\"].sysbench.workload" "$CONFIG_FILE")
tables=$(yq -r ".benchmarks[\"$BENCH\"].sysbench.tables" "$CONFIG_FILE")
table_size=$(yq -r ".benchmarks[\"$BENCH\"].sysbench.table_size // \"\"" "$CONFIG_FILE")
scale=$(yq -r ".benchmarks[\"$BENCH\"].sysbench.scale // \"\"" "$CONFIG_FILE")
threads=${THREADS:-$(yq -r ".benchmarks[\"$BENCH\"].sysbench.threads" "$CONFIG_FILE")}
extra=$(yq -r ".benchmarks[\"$BENCH\"].sysbench.extra // \"\"" "$CONFIG_FILE")
working_dir=$(yq -r ".benchmarks[\"$BENCH\"].sysbench.working_dir // \"\"" "$CONFIG_FILE")
warmup_time=${WARMUP_TIME:-$(yq -r ".benchmarks[\"$BENCH\"].sysbench.warmup_time // 20" "$CONFIG_FILE")}
run_time=${RUN_TIME:-$(yq -r ".benchmarks[\"$BENCH\"].sysbench.run_time // 60" "$CONFIG_FILE")}

# Export environment for docker-compose variable substitution
if [ "$sharded" = "true" ]; then
  export SHARD_1001="-80"
  export SHARDS="-80:1001 80-:2001"
else
  export SHARD_1001="0"
  export SHARDS="0:1001"
fi

export VSCHEMA_FILE="$vschema"
export VTGATE_WORKLOAD_MODE="$vtgate_workload_mode"
export VTGATE_MAX_MEMORY_ROWS="$vtgate_max_memory_rows"
export VTTABLET_EXTRA_FLAGS="$vttablet_extra_flags"
export WORKLOAD="$workload"
export TABLES="$tables"
export TABLE_SIZE="$table_size"
export SCALE="$scale"
export THREADS="$threads"
export EXTRA="$extra"
export WORKING_DIR="$working_dir"
export WARMUP_TIME="$warmup_time"
export RUN_TIME="$run_time"
export PROFILE_ENABLED="$PROFILE"

# Build compose file arguments
COMPOSE_ARGS=(-f "$BENCHMARKS_DIR/docker-compose.yml")
if [ "$sharded" = "true" ]; then
  COMPOSE_ARGS+=(-f "$BENCHMARKS_DIR/docker-compose.sharded.yml")
fi

MYSQL_COMPOSE="$BENCHMARKS_DIR/docker-compose.mysql.yml"

if [ -n "$RUN_NAME" ]; then
  RUN_ID="$(date +%Y%m%d-%H%M%S)-$RUN_NAME"
else
  RUN_ID="$(date +%Y%m%d-%H%M%S)-$$"
fi
export RUN_SUBDIR="$BENCH/$RUN_ID"
RUN_DIR="$BENCHMARKS_DIR/runs/$RUN_SUBDIR"
mkdir -p "$RUN_DIR"

# Unique project and network names so multiple runs can coexist
PROJECT="bench-$RUN_ID"
MYSQL_PROJECT="bench-mysql-$RUN_ID"
export BENCH_NETWORK="bench-$RUN_ID"

cleanup() {
  if [ "$NO_TEARDOWN" = "true" ]; then
    echo "=== Skipping teardown (--no-teardown) ==="
    echo "Cluster is still running. Tear down with:"
    echo "  docker compose --project-directory $BENCHMARKS_DIR ${COMPOSE_ARGS[*]} -p $PROJECT down -v"
    echo "  docker network rm $BENCH_NETWORK"
    return
  fi
  echo "=== Tearing down ==="
  docker compose --project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT" down -v 2>/dev/null || true
  docker compose --project-directory "$BENCHMARKS_DIR" -f "$MYSQL_COMPOSE" -p "$MYSQL_PROJECT" down -v 2>/dev/null || true
  docker network rm "$BENCH_NETWORK" 2>/dev/null || true
}
trap cleanup EXIT

enable_live_contention_profiles() {
  local compose_args services
  compose_args=(--project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT")
  services=$(docker compose "${compose_args[@]}" config --services)

  enable_service_profile() {
    local service="$1" port="$2"
    docker compose "${compose_args[@]}" exec -T "$service" \
      curl -fsS "http://localhost:$port/debug/mutexprofilefraction?fraction=1" >/dev/null
    docker compose "${compose_args[@]}" exec -T "$service" \
      curl -fsS "http://localhost:$port/debug/blockprofilerate?rate=1" >/dev/null
  }

  if printf '%s\n' "$services" | grep -qx 'vtgate'; then
    enable_service_profile "vtgate" "15001"
  fi

  local tablets=()
  while IFS= read -r svc; do
    [ -n "$svc" ] && tablets+=("$svc")
  done < <(printf '%s\n' "$services" | grep '^tablet-' || true)

  for service in "${tablets[@]}"; do
    enable_service_profile "$service" "15100"
  done
}

# Scrape a single pprof endpoint from a service
pprof_scrape() {
  local service="$1" port="$2" endpoint="$3" output="$4"
  docker compose --project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT" \
    exec -T "$service" curl -sS "http://localhost:$port/debug/pprof/$endpoint" > "$output"
}

CPU_PROFILE_PIDS=()

# Start CPU profile collection in background for all services
start_cpu_profiles() {
  local duration="$1"
  local compose_args=(--project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT")
  local services
  services=$(docker compose "${compose_args[@]}" config --services)

  if printf '%s\n' "$services" | grep -qx 'vtgate'; then
    pprof_scrape vtgate 15001 "profile?seconds=$duration" "$RUN_DIR/vtgate_cpu.pprof" &
    CPU_PROFILE_PIDS+=($!)
  fi

  while IFS= read -r service; do
    [ -n "$service" ] || continue
    pprof_scrape "$service" 15100 "profile?seconds=$duration" "$RUN_DIR/${service}_cpu.pprof" &
    CPU_PROFILE_PIDS+=($!)
  done < <(printf '%s\n' "$services" | grep '^tablet-' || true)
}

# Scrape point-in-time profiles (heap, mutex, block, goroutine)
scrape_snapshot_profiles() {
  local compose_args=(--project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT")
  local services pids=()
  services=$(docker compose "${compose_args[@]}" config --services)

  if printf '%s\n' "$services" | grep -qx 'vtgate'; then
    for p in heap mutex block goroutine; do
      pprof_scrape vtgate 15001 "$p" "$RUN_DIR/vtgate_${p}.pprof" &
      pids+=($!)
    done
  fi

  while IFS= read -r service; do
    [ -n "$service" ] || continue
    for p in heap mutex block goroutine; do
      pprof_scrape "$service" 15100 "$p" "$RUN_DIR/${service}_${p}.pprof" &
      pids+=($!)
    done
  done < <(printf '%s\n' "$services" | grep '^tablet-' || true)

  for pid in "${pids[@]}"; do
    wait "$pid"
  done
}

compare_results() {
  local vitess_file="$1"
  local mysql_file="$2"

  extract_tps() { jq '.tps' "$1"; }
  extract_qps() { jq '.qps.total' "$1"; }
  extract_p95() { jq '.latency' "$1"; }

  local v_tps m_tps v_qps m_qps v_p95 m_p95
  v_tps=$(extract_tps "$vitess_file")
  m_tps=$(extract_tps "$mysql_file")
  v_qps=$(extract_qps "$vitess_file")
  m_qps=$(extract_qps "$mysql_file")
  v_p95=$(extract_p95 "$vitess_file")
  m_p95=$(extract_p95 "$mysql_file")

  echo ""
  echo "=== Vitess vs MySQL Comparison ==="
  echo ""
  printf "%-20s %12s %12s %10s\n" "Metric" "Vitess" "MySQL" "Delta"
  printf "%-20s %12s %12s %10s\n" "--------------------" "------------" "------------" "----------"

  print_row() {
    local label="$1" v="$2" m="$3"
    local delta
    if [ -n "$v" ] && [ -n "$m" ]; then
      delta=$(awk "BEGIN {if ($m > 0) printf \"%+.1f%%\", ($v - $m) / $m * 100; else print \"N/A\"}")
      printf "%-20s %12s %12s %10s\n" "$label" "$v" "$m" "$delta"
    fi
  }

  print_row "Transactions/sec" "$v_tps" "$m_tps"
  print_row "Queries/sec" "$v_qps" "$m_qps"
  print_row "P95 latency (ms)" "$v_p95" "$m_p95"
  echo ""
}

parse_sysbench_output() {
  local input="$1" output="$2"
  awk '
    /transactions:/ && /per sec/ {
      for (i = 1; i <= NF; i++) {
        if (index($i, "(") > 0) {
          sub(/\(/, "", $i)
          tps = $i + 0
          break
        }
      }
    }
    /queries:/ && /per sec/ && !/performed/ {
      for (i = 1; i <= NF; i++) {
        if (index($i, "(") > 0) {
          sub(/\(/, "", $i)
          qps = $i + 0
          break
        }
      }
    }
    /95th percentile:/ {
      p95 = $NF + 0
    }
    END {
      printf "{\"tps\": %.2f, \"qps\": {\"total\": %.2f}, \"latency\": %.2f}\n", tps, qps, p95
    }
  ' "$input" > "$output"
}

run_sysbench_all() {
  local output_file="$1"
  shift

  : > "$output_file"

  set +e
  docker compose --project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT" \
    run --quiet --rm "$@" sysbench all 2>&1 | tee "$output_file"
  local run_status=$?
  set -e

  return "$run_status"
}

to_rfc3339() {
  local millis="$1"
  local seconds=$((millis / 1000))
  if date -u -r "$seconds" +"%Y-%m-%dT%H:%M:%SZ" >/dev/null 2>&1; then
    date -u -r "$seconds" +"%Y-%m-%dT%H:%M:%SZ"
  else
    date -u -d "@$seconds" +"%Y-%m-%dT%H:%M:%SZ"
  fi
}

sanitize_profile_type() {
  printf '%s' "$1" | tr ':/' '__'
}

pyroscope_exec() {
  docker compose --project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT" exec -T pyroscope "$@"
}

pyroscope_profile_types() {
  local service="$1" from_time="$2" to_time="$3"
  pyroscope_exec profilecli query series \
    --query="{service_name=\"$service\"}" \
    --from="$from_time" \
    --to="$to_time" \
    --output=json 2>/dev/null |
    jq -r '.series[]?["__profile_type__"]' |
    sort -u
}

wait_for_host_file() {
  local file="$1"
  local retries="${2:-50}"
  while [ ! -f "$file" ] && [ "$retries" -gt 0 ]; do
    sleep 0.2
    retries=$((retries - 1))
  done
  [ -f "$file" ]
}

export_profiles_from_pyroscope() {
  local started_file="$RUN_DIR/.benchmark-started"
  local done_file="$RUN_DIR/.benchmark-done"
  wait_for_host_file "$started_file"
  wait_for_host_file "$done_file"

  local from_time to_time service profile_type output_file
  from_time="$(to_rfc3339 "$(cat "$started_file")")"
  to_time="$(to_rfc3339 "$(cat "$done_file")")"

  local services=("vtgate")
  for pair in $SHARDS; do
    tablet_id="${pair##*:}"
    services+=("tablet-$tablet_id")
  done

  # Wait until Pyroscope has indexed the benchmark interval.
  local retries=30
  local have_types=""
  while [ "$retries" -gt 0 ]; do
    have_types="$(pyroscope_profile_types "vtgate" "$from_time" "$to_time" || true)"
    [ -n "$have_types" ] && break
    sleep 1
    retries=$((retries - 1))
  done

  if [ -z "$have_types" ]; then
    echo "Error: Pyroscope did not return any profile types for vtgate in $from_time..$to_time" >&2
    return 1
  fi

  echo "=== Exporting profiles from Pyroscope ==="
  for service in "${services[@]}"; do
    while IFS= read -r profile_type; do
      [ -n "$profile_type" ] || continue
      output_file="/vt/runs/$RUN_SUBDIR/${service}_$(sanitize_profile_type "$profile_type").pprof"
      pyroscope_exec profilecli query profile \
        --query="{service_name=\"$service\"}" \
        --profile-type="$profile_type" \
        --from="$from_time" \
        --to="$to_time" \
        --output="pprof=$output_file" >/dev/null </dev/null
    done < <(pyroscope_profile_types "$service" "$from_time" "$to_time" || true)
  done

  if ! compgen -G "$RUN_DIR/*.pprof" >/dev/null; then
    echo "Error: Pyroscope export completed without writing any .pprof files to $RUN_DIR" >&2
    return 1
  fi
}

kv() { printf "  %-16s %s\n" "$1" "$2"; }

echo ""
echo "=== Benchmark: $BENCH ==="
echo ""

echo "Vitess:"
if [ "$sharded" = "true" ]; then
  kv "Topology:" "sharded (2 shards)"
else
  kv "Topology:" "unsharded"
fi
[ -n "$vtgate_workload_mode" ] && kv "Mode:" "$vtgate_workload_mode"
kv "VSchema:" "$vschema"
[ -n "$vtgate_max_memory_rows" ] && kv "Max mem rows:" "$vtgate_max_memory_rows"
[ -n "$vttablet_extra_flags" ] && kv "Tablet flags:" "$vttablet_extra_flags"

echo ""
echo "Sysbench:"
kv "Workload:" "$workload"
kv "Tables:" "$tables"
[ -n "$table_size" ] && kv "Table size:" "$table_size"
[ -n "$scale" ] && kv "Scale:" "$scale"
kv "Threads:" "$threads"
kv "Warmup:" "${warmup_time}s"
kv "Run time:" "${run_time}s"

echo ""
kv "Profile:" "$PROFILE"
kv "MySQL cmp:" "$MYSQL_MODE"
kv "No teardown:" "$NO_TEARDOWN"
kv "Run dir:" "runs/$BENCH/$(basename "$RUN_DIR")"
echo ""

# Create an isolated network for this run
docker network create "$BENCH_NETWORK" 2>/dev/null || true

# Run sysbench once and let Compose start the Vitess dependency graph.
echo "=== Running Vitess benchmark ==="
run_sysbench_all "$RUN_DIR/vitess.txt"
parse_sysbench_output "$RUN_DIR/vitess.txt" "$RUN_DIR/vitess.json"

if [ "$PROFILE" = "true" ]; then
  export_profiles_from_pyroscope
fi

# Run MySQL comparison if requested
if [ "$MYSQL_MODE" = "true" ]; then
  # Tear down Vitess to free resources
  echo "=== Tearing down Vitess cluster ==="
  docker compose --project-directory "$BENCHMARKS_DIR" "${COMPOSE_ARGS[@]}" -p "$PROJECT" down -v

  # Start plain MySQL
  echo "=== Starting plain MySQL ==="
  docker compose --project-directory "$BENCHMARKS_DIR" -f "$MYSQL_COMPOSE" -p "$MYSQL_PROJECT" up -d

  # Wait for MySQL to be ready
  echo "=== Waiting for MySQL ==="
  while ! docker compose --project-directory "$BENCHMARKS_DIR" -f "$MYSQL_COMPOSE" -p "$MYSQL_PROJECT" \
    exec -T mysql mysql -u root -e "SELECT 1" &>/dev/null; do
    sleep 2
  done
  echo "MySQL is ready."

  # Run sysbench against plain MySQL in a single container invocation.
  echo "=== Running MySQL benchmark ==="
  run_sysbench_all "$RUN_DIR/mysql.txt" --no-deps -e HOST=mysql -e PORT=3306
  parse_sysbench_output "$RUN_DIR/mysql.txt" "$RUN_DIR/mysql.json"

  # Print comparison
  compare_results "$RUN_DIR/vitess.json" "$RUN_DIR/mysql.json"
fi

echo "=== Benchmark complete ==="
echo "Run saved to runs/$BENCH/$(basename "$RUN_DIR")"
