// Command memcheck measures runtime memory footprint of each candidate
// at steady state. We force GC twice before each measurement to settle
// the heap, then compute (HeapAlloc after build) - (HeapAlloc baseline).
//
// This is the headline memory number for the article. Static estimates
// from MemBytes() are a sanity check, not the source of truth.
package main

import (
	"fmt"
	"runtime"

	"mmrbench/pkg/index"
	"mmrbench/pkg/index/fenwick"
	"mmrbench/pkg/index/hybrid"
	"mmrbench/pkg/index/skiplist"
	"mmrbench/pkg/index/sortedarr"
	"mmrbench/pkg/workload"
)

const N = 100_000 // in-env preview size; runner overrides for c7i

type candidate struct {
	name string
	make func() index.Index
}

func measure(c candidate, dist workload.Distribution) {
	// Settle baseline.
	runtime.GC()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	idx := c.make()
	gen := workload.New(dist, 1)
	for i := 0; i < N; i++ {
		idx.Add(uint64(i+1), gen.SampleMMR())
	}

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)

	delta := after.HeapAlloc - before.HeapAlloc
	staticEst := idx.MemBytes()
	bytesPerPlayer := float64(delta) / float64(N)
	fmt.Printf("  %-22s heap=%10d B  static_est=%10d B  per_player=%5.1f B\n",
		c.name+"/"+dist.String(), delta, staticEst, bytesPerPlayer)

	// Keep idx live so it isn't reclaimed before the measurement.
	runtime.KeepAlive(idx)
}

func main() {
	cands := []candidate{
		{"fenwick", func() index.Index { return fenwick.NewSized(N) }},
		{"sortedarr", func() index.Index { return sortedarr.NewSized(N) }},
		{"skiplist", func() index.Index { return skiplist.NewSized(N) }},
		{"skiplist-pooled", func() index.Index { return skiplist.NewPooledSized(N) }},
		{"hybrid", func() index.Index { return hybrid.NewSized(N) }},
	}
	dists := []workload.Distribution{
		workload.Uniform,
		workload.Skewed,
		workload.HotRange,
	}

	fmt.Printf("Memory footprint at N=%d players (post-GC heap delta):\n\n", N)
	for _, d := range dists {
		for _, c := range cands {
			measure(c, d)
		}
		fmt.Println()
	}
}
