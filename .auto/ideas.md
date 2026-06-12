# Ideas backlog

Ranked by expected value. Detection limit is ~3-4% (cluster-to-cluster noise);
anything smaller must be batched or measured with multiple clusters.

- **Query fingerprint fast-path in vtgate** (~+5-7%): parse+normalize+String
  costs ~8.5µs/query just to compute the plan-cache key from raw SQL with
  literals. Tokenizer-only scan could produce normalized text + bindvars in
  ~1-2µs. Safety: on first occurrence of a fingerprint run BOTH paths, verify
  equivalence (normalized SQL + bindvars), cache a "fastpath-safe" flag per
  shape; bail on comments/directives/unusual tokens.
- **Tablet BEGIN piggyback** (~+4%): tabletserver BeginExecute does a
  "start transaction" mysqld round trip (~50µs with busy-tax) before the first
  query, 2x per txn. Combine into one round trip via multi-statement exec
  ("start transaction; <query>") in the BeginExecute path. Check
  ExecuteFetchMulti + CLIENT_MULTI_STATEMENTS support in connpool dbconfigs,
  and savepoint/settings interplay in TxPool.Begin.
- **Pipelined multi-shard ops on one goroutine** (~+3-5%): split fastquery
  Pool.Execute into send/recv halves; scatter queries and multi-shard commits
  could write both requests then read both responses from the calling
  goroutine — zero goroutine spawns (exp2/exp14 showed spawn cost cancels
  parallelism gains on GOMAXPROCS=2).
- **mysql text-row pass-through** (big, +5-10%?): vttablet executes via text
  protocol; mysql row packets are [len][bytes] per column — nearly identical
  to the fastquery result codec. Tablet could frame rows straight from the
  mysqld wire without building sqltypes.Result; vtgate could write rows to
  the client almost verbatim for non-transformed single-shard results.
  exp11's codec is the first step in this direction.
- **vtgate alloc/CPU trim batch** (~+2-4% combined, sub-noise individually):
  LogStats (270MB), VCursorImpl (233MB), ExpressionEnv (214MB),
  ResolveDestinations closure (223MB) per-query allocs; sync.Pool or reuse.
  nanotime/time.Now ~5% CPU from stats Timings.Record layers.
- **vtproto unmarshal_unsafe codegen feature**: would allow zero-copy proto
  unmarshal on remaining grpc paths (needs `make proto` with
  features=...+unmarshal_unsafe; check planetscale/vtprotobuf version).
- **ctx deadline propagation for fastquery**: carry a deadline field in the
  request frame; needed before upstreaming.
- **TLS support for fastquery**: needed before upstreaming.
