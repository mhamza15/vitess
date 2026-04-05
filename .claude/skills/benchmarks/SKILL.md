---
name: benchmarks
description: Use when running Vitess sysbench benchmarks, profiling Vitess performance, comparing Vitess vs MySQL, or debugging benchmark infrastructure in the benchmarks/ directory.
---

# Running Benchmarks

Local sysbench benchmarks against a dockerized Vitess cluster. Config lives in `benchmarks/benchmarks.yml`, infrastructure in `benchmarks/`.

## Quick Reference

| Command | What it does |
|---------|-------------|
| `make bench BENCH=oltp` | Run benchmark |
| `make bench BENCH=oltp PROFILE=1` | Run with pprof profiling |
| `make bench BENCH=oltp KEEP=1` | Keep cluster up after run |
| `make bench BENCH=oltp MYSQL=1` | Run against Vitess and MySQL, compare |
| `make bench BENCH=oltp TIME=15 WARMUP=5` | Override run/warmup seconds |
| `make bench-build` | Build docker images only |
| `make bench-clean` | Tear down everything |

## Available Workloads

| Name | Sharded | Workload | Threads |
|------|---------|----------|---------|
| `oltp` | yes | oltp_read_write | 42 |
| `oltp-readonly` | yes | oltp_read_only | 42 |
| `olap` | yes | oltp_read_only (OLAP mode) | 42 |
| `olap-sort` | yes | olap_sort | 16 |
| `tpcc` | yes | tpcc.lua | 42 |
| `tpcc-olap` | yes | tpcc.lua (OLAP mode) | 42 |
| `tpcc-unsharded` | no | tpcc.lua | 42 |

## Profiling

`PROFILE=1` enables direct pprof scraping during the benchmark:

- **CPU profiles** are collected in the background for the full benchmark duration (warmup + run time) via `/debug/pprof/profile?seconds=N`
- **Snapshot profiles** (heap, mutex, block, goroutine) are scraped immediately after the benchmark finishes, while the cluster is still running
- Mutex and block profiling are enabled on all services before the benchmark starts

All profiles are saved as standard `.pprof` files to the run directory `benchmarks/runs/<bench>/<timestamp>/`:
```
vtgate_cpu.pprof
vtgate_heap.pprof
vtgate_mutex.pprof
vtgate_block.pprof
vtgate_goroutine.pprof
tablet-1001_cpu.pprof
tablet-1001_heap.pprof
...
```

Analyze with `go tool pprof`:
```bash
go tool pprof -http=:8080 benchmarks/runs/oltp/20260316-123456/vtgate_cpu.pprof
```

Without `PROFILE=1`, no profiling is performed.

## Architecture

```
benchmarks/
  benchmarks.yml              # Workload definitions
  run.sh                      # Orchestrates: build, start, bench, teardown
  docker-compose.yml          # Base compose (etcd, vtctld, tablet-1001, vtgate, sysbench)
  docker-compose.sharded.yml  # Adds second shard (tablet-2001) for sharded benchmarks
  docker-compose.mysql.yml    # Plain MySQL for comparison mode
  runs/<bench>/<timestamp>/   # Per-run output (pprof files, JSON results)
```

`run.sh` reads `benchmarks.yml`, exports config as env vars for docker-compose variable substitution, then orchestrates the cluster + sysbench.

## Adding a Workload

Add an entry to `benchmarks.yml`. The shared compose files use env var substitution, so no per-workload generation is needed.
