# Autoresearch: Vitess oltp QPS vs MySQL

## Objective
Vitess significantly underperforms vanilla MySQL on the sysbench benchmark suite
in `benchmarks/`. Increase Vitess QPS and reduce p95 latency on the `oltp`
workload (sysbench `oltp_read_write`, 10 tables x 10k rows, 1 thread, sharded
2-shard cluster) to close the gap with MySQL. Always run with full profiling
(`PROFILE=1`, Pyroscope) and use the exported pprof profiles to pick the
highest-ROI optimization targets.

## Metrics
- **Primary**: `qps` (queries/sec, higher is better) — optimization target
- **Secondary**: `tps` (transactions/sec, higher is better), `p95_ms` (p95 latency ms, lower is better)
- **Target**: MySQL baseline numbers recorded in "Baselines" below

## How to Run
`./.auto/measure.sh` — builds the vitess-bench docker image from the working
tree, runs `make bench BENCH=oltp PROFILE=1`, and prints:
- `RUN_DIR benchmarks/runs/oltp/<id>` — contains `vitess.txt` and exported
  `.pprof` files (vtgate + tablet-1001 + tablet-2001: cpu, heap, mutex, block, goroutine)
- `METRIC qps=...`, `METRIC tps=...`, `METRIC p95_ms=...`

Analyze profiles after every run, e.g.:
`go tool pprof -top -nodecount=30 <RUN_DIR>/vtgate_process_cpu_cpu_nanoseconds_cpu.pprof`
(profile file names come from Pyroscope profile types; `ls <RUN_DIR>` first).

Each iteration takes ~5 min (docker image build + cluster startup + 20s warmup
+ 60s run). One run per iteration; watch the noise floor across runs.

To get a fresh MySQL comparison: `make bench BENCH=oltp MYSQL=1` (slow; the
MySQL number is a fixed target — no need to re-run it every iteration).

## Architecture Notes
- Topology: etcd + vtctld + vtgate + 2 shards (tablet-1001/2001, each with its
  own mysqld). Each service is CPU-limited (vtgate 2 CPUs, tablets 2 CPUs,
  mysqld 2 CPUs) via docker compose.
- vtgate and vttablet run with `GOGC=off` / `GOMEMLIMIT=1GiB` already.
- sysbench runs single-threaded → latency-bound workload. Every per-query
  overhead (parse, plan, RPC hop vtgate→vttablet, grpc serialization) directly
  reduces QPS. The vtgate→vttablet→mysqld double hop is the structural handicap
  vs direct MySQL.
- Query path: client → vtgate (parse/plan/route) → grpc → vttablet
  (query engine, connection pools) → mysqld.

## Files in Scope
Vitess Go source, especially the per-query hot path:
- `go/vt/vtgate/` — Executor, plan cache, scatter_conn, session handling
- `go/vt/vtgate/engine/` — plan execution primitives
- `go/vt/vtgate/planbuilder/`, `go/vt/sqlparser/` — parsing/planning (cached plans should make this cold; verify in profiles)
- `go/vt/vttablet/tabletserver/` — query engine, connection pools, query rules
- `go/vt/vttablet/grpcqueryservice/`, `go/vt/vtgate/vtgateservice/`, grpc client/server tunables
- `go/mysql/` — MySQL protocol encode/decode (vtgate front-end and tablet back-end)
- `benchmarks/` service entrypoints/flags (`benchmarks/vtgate/`, `benchmarks/tablet/`, compose files) — flag tuning is fair game, but keep the workload definition (sysbench parameters, durations, threads, CPU limits) unchanged

## Off Limits
- `benchmarks/benchmarks.yml` sysbench parameters, run/warmup times, thread counts, docker CPU/memory limits — changing the workload is cheating
- `benchmarks/run.sh` metric parsing/reporting
- Generated code (`go/vt/proto/`, files with `Code generated` headers) — edit sources and regenerate instead
- Anything that breaks correctness: sysbench oltp_read_write must complete without errors

## Constraints
- `git checkout` of sysbench results: a run with sysbench errors (non-zero
  ignored errors / failed queries) must be discarded even if QPS improved
- Code must compile (`go build ./go/...`); measure.sh pre-checks vtgate/vttablet
- Unit tests for changed packages should pass before keeping:
  `go test ./go/vt/<changed-pkg>/...`
- Prefer simple, upstreamable changes; flag tuning in benchmark entrypoints is
  acceptable but note it separately from code wins

## Baselines
(filled in by the first experiment)
- Vitess oltp: TBD
- MySQL oltp: TBD

## What's Been Tried
(nothing yet)
