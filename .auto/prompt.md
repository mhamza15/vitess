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
tree, runs `make bench BENCH=oltp PROFILE=1` with `BENCH_REPEAT=3` (sysbench
run-phase executed 3x in one cluster, prepare once), reports the MEDIAN, and prints:
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
**The entire Vitess repository is in scope** — any file may be modified (except
the Off Limits items below). The per-query hot path is the most promising
starting point:
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
- **Backwards compatibility does not matter.** Changing commit order, removing
  compatibility wrappers, altering public APIs/semantics is all allowed if it
  makes the hot path faster (user directive 2026-06-11).
- `git checkout` of sysbench results: a run with sysbench errors (non-zero
  ignored errors / failed queries) must be discarded even if QPS improved
- Code must compile (`go build ./go/...`); measure.sh pre-checks vtgate/vttablet
- Unit tests for changed packages should pass before keeping:
  `go test ./go/vt/<changed-pkg>/...`
- Prefer simple, upstreamable changes; flag tuning in benchmark entrypoints is
  acceptable but note it separately from code wins

## Baselines
From `runs/oltp/20260611-184816-auto-baseline` (PROFILE=1, MYSQL=1):
- Vitess oltp: qps=4966.02, tps=248.31, p95_ms=4.74
- MySQL oltp:  qps=46805.10, tps=2340.26, p95_ms=0.50
- Gap: Vitess is at ~10.6% of MySQL throughput; p95 is ~9.5x worse.
  Single-threaded sysbench → pure latency problem: ~4.3ms of overhead per
  transaction (20 queries) added by the vtgate→vttablet→mysqld path.

## Measurement Noise
- Within-cluster sample spread: ~1% (3 repeats in one cluster are tight)
- Cluster-to-cluster spread: ±4-5% — the dominant noise source. Single-run
  comparisons across clusters cannot detect <10% effects. Always use the
  3-sample median; for borderline results, re-run the full cluster.

## Latency Budget (measured 2026-06-11, per query ~200µs end-to-end)
- vtgate mysql-protocol front + executor: ~30µs (parse+normalize ~8µs; VtgateApi mean 173µs)
- grpc hop vtgate→tablet: ~95µs (TCP RTT floor 30-45µs, rest is grpc-go + scheduler wakeups)
- vttablet engine: ~3µs
- vttablet→mysqld Go client layer: ~40µs gap (64µs observed under load vs 12.6µs
  for the same go/mysql client in an idle process — it's scheduler/wakeup tax, not client code)
- raw mysqld (unix socket): point=10µs, ranges=20-84µs, update=11µs
- Per-query CPU is ON the critical path (single-threaded): vtgate ~65-70µs CPU/query,
  tablet ~40µs — cutting CPU cuts latency ~1:1
- No CFS throttling under normal load (nr_throttled=0)

## What's Been Tried
- **#2 parallel multi-shard commit** (tx_conn.go): no gain (4977/4898 vs ~5016 vanilla).
  Goroutine spawn+handoff on 2-P runtime eats the saved serial RTT. DISCARDED.
- **#3 scheduler busy-spin goroutine** (VT_SCHED_SPIN): 8x WORSE (662 qps, p95 42ms).
  Busy-spin exhausts CFS quota → ms-scale throttling. NEVER busy-spin under cpu limits. DISCARDED.
- **#4 diagnostic**: go/mysql client = 12.6µs idle-process vs 64µs in busy vttablet →
  overhead is environmental (wakeups), not algorithmic.
- **#5 grpc.NumStreamWorkers(GOMAXPROCS)** on servenv grpc server: **+6.8% qps**
  (5288 vs 4953 median-of-3 A/B), p95 4.65→4.33. KEPT.
- **#6 GOGC=200, drop GOMEMLIMIT** (was GOGC=off): **+2.5%** (5424). KEPT (compose env).
- **#7 grpc bidi-stream Execute pipe** (queryservice.QueryPipe): **+5.9%** (5747),
  p95 4.03. KEPT — now mostly superseded by fastquery but remains the grpc fallback.
- **#8 pipe for BeginExecute/Commit**: dead even at the time. DISCARDED — but see #10.
- **#9 fastquery raw TCP transport for Execute** (VT_FASTQUERY, port=grpc+1):
  **+17-25%** (7167/6344). Raw TCP echo floor between containers is 5-12µs vs grpc
  hop ~95µs; kills HTTP/2 framing + all goroutine handoffs. KEPT.
- **#10 fastquery BeginExecute+Commit**: **+15.8%** (8303, p95 2.91). KEPT.
  Lesson: relative weights shift as you optimize — revisit discarded ideas.

## Current state (2026-06-11 21:26)
- 8303 qps / 415 tps / p95 2.91ms — cumulative +67% from 4966 baseline.
- MySQL target: 46805 qps. Per-client-query budget now ~120µs (was 200µs).
- Hot-path transport is fastquery (raw TCP) for Execute/BeginExecute/Commit;
  everything else still grpc. Caveats: no TLS/auth on fastquery, ctx deadline
  not propagated to tablet, server uses context.Background() per request.
