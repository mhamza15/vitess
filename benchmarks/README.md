# Local Benchmarks

Run sysbench benchmarks against your local Vitess working tree with full observability (Prometheus, Grafana, Pyroscope).

## Prerequisites

- Docker and Docker Compose
- [yq](https://github.com/mikefarah/yq) (`brew install yq` on macOS)
- ~8 GB RAM available for Docker

## Quick Start

```bash
# Run the OLTP benchmark (builds images, generates configs, runs benchmark)
make bench BENCH=oltp

# Run with CPU/heap profiling
make bench BENCH=oltp PPROF=1

# Keep the cluster running after the benchmark
make bench BENCH=oltp KEEP=1

# Tear everything down
make bench-clean
```

## Available Workloads

| Workload | Description | Sharding |
|----------|-------------|----------|
| `oltp` | OLTP read/write mix (sysbench oltp_read_write) | 2 shards |
| `oltp-readonly` | OLTP read/write with SET statements | 2 shards |
| `olap` | OLAP read-only (OLAP workload mode) | 2 shards |
| `olap-sort` | OLAP with GROUP BY + ORDER BY | 2 shards |
| `tpcc` | TPC-C via sysbench-tpcc | 2 shards |
| `tpcc-unsharded` | TPC-C on single unsharded keyspace | 1 shard |

## How It Works

1. `make docker_lite` builds `vitess/lite` from your local source
2. `vitess-bench.Dockerfile` layers mysqld_exporter + gosu on top
3. `sysbench.Dockerfile` builds planetscale/sysbench from source
4. Docker Compose orchestrates: etcd, vtctld, mysql, vttablet, vtgate, sysbench
5. Monitoring stack runs alongside: Prometheus, Grafana, Pyroscope, Alloy

## Observability

While benchmarks run (or with `KEEP=1`):

- **Grafana**: http://localhost:3000 (pre-built "Vitess Benchmark" dashboard)
- **Prometheus**: http://localhost:9090
- **Pyroscope**: http://localhost:4040 (continuous CPU/memory/mutex profiling)

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make bench BENCH=<name>` | Build + generate + run a benchmark |
| `make bench-build` | Build vitess-bench and sysbench images |
| `make bench-generate` | Regenerate all benchmark configs |
| `make bench-clean` | Tear down all containers and networks |

## Configuration

Workloads are defined in `benchmarks.yml`. Each workload specifies:
- **vitess**: sharding, vschema, vtgate workload mode, extra flags
- **sysbench**: workload script, table count/size, threads, run time

`generate.sh` reads this config and produces per-workload Docker Compose files under `workloads/`.

## Directory Structure

```
benchmarks/
├── benchmarks.yml              # Workload definitions
├── generate.sh                 # Generates per-benchmark compose files
├── vitess-bench.Dockerfile     # Extends vitess/lite
├── sysbench.Dockerfile         # Builds sysbench from source
├── docker-compose.monitor.yml  # Prometheus + Grafana + Pyroscope
├── prometheus/                 # Base Prometheus config
├── grafana/                    # Dashboards + provisioning
├── alloy/                      # Pyroscope scrape configs
├── sysbench/                   # Entrypoint + custom Lua workloads
├── mysql/                      # mysqlctld entrypoint
├── tablet/                     # vttablet entrypoint + MySQL tuning
├── vtctld/                     # vtctld entrypoint
├── vtctld-init/                # Shard init + VSchema apply
├── vtgate/                     # vtgate entrypoint
├── vschema/                    # VSchema definitions
└── workloads/                  # Generated per-benchmark dirs
    ├── oltp/
    ├── oltp-readonly/
    ├── olap/
    ├── olap-sort/
    ├── tpcc/
    └── tpcc-unsharded/
```
