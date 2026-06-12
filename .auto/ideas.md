# Ideas backlog

- **Bidi-stream Execute pipe**: replace per-query unary Execute RPC
  (vtgate→vttablet) with a long-lived bidirectional grpc stream per tablet,
  multiplexing requests by id. Kills per-call stream setup (HEADERS frames,
  HPACK, stream accounting, ~1GB/85s allocs in newClientStream) and halves
  loopy-writer wakeups. Expected +8-15%. Big change: proto + grpcqueryservice
  + grpctabletconn + session/error handling.
- **vtgate result pass-through**: tablet QueryResult proto → sqltypes.Result →
  mysql wire conversion allocates ~30% of vtgate heap. A fast path writing
  mysql wire format straight from the proto rows would cut ~10-15µs CPU/query.
- **Stats/timing overhead**: nanotime+time.now ≈5% CPU on both vtgate and
  tablet (~3.5µs/query each side). Audit Timings.Record/time.Now call count
  per query; batch or drop redundant ones.
- **Tablet-side execRequest wrapper trimming**: TabletServer.execute →
  QueryExecutor chain has multiple context/span/stats layers; ~11µs CPU/query.
- **Parallel commit retest after handoff fixes**: parallel multi-shard commit
  showed no gain at baseline; retest once goroutine handoffs are cheaper
  (NumStreamWorkers already in). May combine with commit RPC coalescing.
