# mmr-benchmarks

A Go benchmark suite for the data structure underneath a matchmaking queue.
It compares four candidates — sorted array, skip list (Redis-style ZSET),
Fenwick tree, and a Fenwick + per-bucket hybrid — on the three operations a
rank-based matchmaker actually performs:

- **Range count** — how many players have MMR in `[lo, hi]` (~89% of ops)
- **Rank query** — exact global rank for a given MMR (~9% of ops)
- **Update** — add/remove a player from the queue (~2% of ops)

The headline result: Fenwick is ~35× faster than a skip list on queries,
~8× faster on updates, and uses ~3× less memory. Full write-up with
methodology, distributions, p50/p99 numbers, scaling curves, and a
decision table is in [ARTICLE_RESEARCH.md](ARTICLE_RESEARCH.md).

## Layout

```
pkg/index/            Index interface every candidate implements
pkg/index/fenwick/    Fenwick tree (range-count only)
pkg/index/hybrid/     Fenwick + per-bucket player lists + ID map
pkg/index/skiplist/   Skip list (Redis ZSET equivalent, with sync.Pool variant)
pkg/index/sortedarr/  Sorted slice with binary search (baseline)
pkg/workload/         Uniform / Skewed / HotRange MMR generators
pkg/oracle/           Brute-force reference used by the correctness gate
pkg/measure/          Latency histogram (p50/p99/p99.9)
bench/                testing.B isolated-op benchmarks
cmd/correctness/      Gate every candidate against the oracle
cmd/memcheck/         Heap-delta at steady state
cmd/percentile/       Mixed-workload p50/p99/p99.9 at production op mix
cmd/scaleup/          Per-op latency vs N (100K → 1M)
charts/               Article charts + the Python script that draws them
```

The `Index` interface lives in `pkg/index/index.go:25` — that's the contract
any new candidate has to satisfy.

## Reproducing the numbers

Requires Go 1.22+.

```sh
go run ./cmd/correctness   # gate every candidate against the brute-force
go run ./cmd/memcheck      # heap delta at steady state
go test -bench=. ./bench   # isolated-op latencies via testing.B
go run ./cmd/percentile    # mixed-workload p50/p99/p99.9
go run ./cmd/scaleup       # scaling from 100K to 1M
```

All commands use deterministic seeds, so re-runs on the same host give
identical numbers. Absolute latencies depend on the host — the article's
table is from a 1-vCPU Xeon at 2.8 GHz; ratios between candidates should
hold across machines.

## Tuning parameters

Workload mix, MMR range, distribution parameters, and steady-state N are
hardcoded so every reader benches the same thing. They live in:

- `pkg/index/index.go` — `MaxMMR` (Fenwick bucket count)
- `pkg/workload/gen.go` — distribution shapes
- `cmd/percentile/main.go` — op mix (1.78% / 8.93% / 89.29%) and `steadyN`
- `cmd/scaleup/main.go` — population sweep points

Changing them requires editing source and recompiling — that's deliberate.

## When this benchmark applies

The candidate ranking here assumes integer-quantizable MMR in a bounded
range (~5,000 buckets), a write-light read-heavy mix, and a single-host
shard. The "When NOT to use this" section of the article covers what
breaks each of those assumptions; the trade-off table near the end maps
common workload shapes to the right structure.
