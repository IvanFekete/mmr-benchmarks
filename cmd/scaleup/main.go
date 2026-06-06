// Command scaleup measures how each candidate's per-op latency scales
// with steady-state population N. The article's claim is that Fenwick
// stays roughly constant (bounded by log B = log of bucket count, not N)
// while skip list degrades through cache pressure as the heap working
// set grows past L2/L3.
//
// This run uses one distribution (uniform) and one op at a time, focused
// on the scaling curve rather than candidate-vs-candidate at a fixed N.
package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"mmrbench/pkg/index"
	"mmrbench/pkg/index/fenwick"
	"mmrbench/pkg/index/hybrid"
	"mmrbench/pkg/index/skiplist"
	"mmrbench/pkg/index/sortedarr"
	"mmrbench/pkg/workload"
)

// benchOp runs op() repeatedly until we accumulate ~targetDuration of
// work, then returns ns/op. Auto-scales iteration count.
func benchOp(op func(i int), targetDuration time.Duration) int64 {
	// Warmup.
	for i := 0; i < 1000; i++ {
		op(i)
	}
	iters := 10_000
	for {
		start := time.Now()
		for i := 0; i < iters; i++ {
			op(i)
		}
		elapsed := time.Since(start)
		if elapsed >= targetDuration {
			return elapsed.Nanoseconds() / int64(iters)
		}
		// Scale up: target / current = factor, with 20% headroom.
		factor := int(targetDuration*120/elapsed/100) + 1
		iters *= factor
	}
}

type benchResult struct {
	candidate  string
	n          int
	rankNs     int64
	rangeNs    int64
	updateNs   int64
	heapBytes  uint64
	bytesPlyr  float64
	buildMs    int64
}

func runForN(name string, mk func() index.Index, n int, includeSortedarr bool) benchResult {
	if name == "sortedarr" && !includeSortedarr {
		return benchResult{candidate: name, n: n}
	}

	runtime.GC()
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	idx := mk()
	gen := workload.New(workload.Uniform, 42)
	buildStart := time.Now()
	for i := 0; i < n; i++ {
		idx.Add(uint64(i+1), gen.SampleMMR())
	}
	buildMs := time.Since(buildStart).Milliseconds()

	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	heapDelta := memAfter.HeapAlloc - memBefore.HeapAlloc

	// Pre-generate query data outside timing.
	queryGen := workload.New(workload.Uniform, 999)
	const qBuf = 4096
	rankQs := make([]int, qBuf)
	for i := range rankQs {
		rankQs[i] = queryGen.SampleMMR()
	}
	rangeQs := make([][2]int, qBuf)
	for i := range rangeQs {
		lo, hi := queryGen.SampleRange()
		rangeQs[i] = [2]int{lo, hi}
	}

	target := 500 * time.Millisecond
	if name == "sortedarr" && n >= 500_000 {
		target = 100 * time.Millisecond // updates take too long at big N
	}

	var sinkI int
	rankNs := benchOp(func(i int) {
		sinkI += idx.Rank(rankQs[i&(qBuf-1)])
	}, target)
	rangeNs := benchOp(func(i int) {
		q := rangeQs[i&(qBuf-1)]
		sinkI += idx.RangeCount(q[0], q[1])
	}, target)
	_ = sinkI

	// Update benchmark: harder because the index size changes. Use balanced
	// remove/add with present[] tracking, like the mixed-workload bench.
	present := make([]uint64, n)
	for i := 0; i < n; i++ {
		present[i] = uint64(i + 1)
	}
	rng := rand.New(rand.NewSource(2))
	nextID := uint64(n + 1)

	updTarget := target
	if name == "sortedarr" {
		updTarget = 100 * time.Millisecond
	}

	updateNs := benchOp(func(i int) {
		victimIdx := rng.Intn(n)
		victim := present[victimIdx]
		idx.Remove(victim)
		nid := nextID
		nextID++
		idx.Add(nid, queryGen.SampleMMR())
		present[victimIdx] = nid
	}, updTarget)

	runtime.KeepAlive(idx)

	return benchResult{
		candidate: name,
		n:         n,
		rankNs:    rankNs,
		rangeNs:   rangeNs,
		updateNs:  updateNs,
		heapBytes: heapDelta,
		bytesPlyr: float64(heapDelta) / float64(n),
		buildMs:   buildMs,
	}
}

func main() {
	scales := []int{100_000, 250_000, 500_000, 1_000_000}
	cands := []struct {
		name string
		make func(int) func() index.Index
	}{
		{"fenwick", func(n int) func() index.Index {
			return func() index.Index { return fenwick.NewSized(n) }
		}},
		{"hybrid", func(n int) func() index.Index {
			return func() index.Index { return hybrid.NewSized(n) }
		}},
		{"skiplist", func(n int) func() index.Index {
			return func() index.Index { return skiplist.NewSized(n) }
		}},
		{"skiplist-pooled", func(n int) func() index.Index {
			return func() index.Index { return skiplist.NewPooledSized(n) }
		}},
		{"sortedarr", func(n int) func() index.Index {
			return func() index.Index { return sortedarr.NewSized(n) }
		}},
	}

	results := make(map[string]map[int]benchResult)
	for _, c := range cands {
		results[c.name] = make(map[int]benchResult)
	}

	fmt.Println("Scaling sweep, uniform distribution, in-env (1 vCPU, Xeon 2.8 GHz, Go 1.22):\n")
	fmt.Printf("%-18s %10s %10s %10s %10s %10s %10s\n",
		"candidate", "N", "build_ms", "rank_ns", "range_ns", "update_ns", "B/player")
	fmt.Println("-----------------------------------------------------------------------------------")

	for _, n := range scales {
		for _, c := range cands {
			// Sortedarr: skip beyond N=250K because O(N²) build.
			include := !(c.name == "sortedarr" && n > 250_000)
			r := runForN(c.name, c.make(n), n, include)
			if !include {
				fmt.Printf("%-18s %10d %10s %10s %10s %10s %10s\n",
					c.name, n, "(skip)", "(skip)", "(skip)", "(skip)", "(skip)")
				continue
			}
			results[c.name][n] = r
			fmt.Printf("%-18s %10d %10d %10d %10d %10d %10.1f\n",
				r.candidate, r.n, r.buildMs, r.rankNs, r.rangeNs, r.updateNs, r.bytesPlyr)
		}
		fmt.Println()
		runtime.GC()
	}

	// Scaling factor table — how much does ns/op grow from 100K to 1M?
	fmt.Println("\nScaling factor (ns/op at N=1M divided by N=100K):")
	fmt.Printf("%-18s %10s %10s %10s\n", "candidate", "rank ×", "range ×", "update ×")
	fmt.Println("--------------------------------------------------------")
	for _, c := range cands {
		base, baseOk := results[c.name][100_000]
		large, largeOk := results[c.name][1_000_000]
		if !baseOk || !largeOk || base.rankNs == 0 || large.rankNs == 0 {
			fmt.Printf("%-18s     (incomplete data)\n", c.name)
			continue
		}
		fmt.Printf("%-18s %10.2f %10.2f %10.2f\n",
			c.name,
			float64(large.rankNs)/float64(base.rankNs),
			float64(large.rangeNs)/float64(base.rangeNs),
			float64(large.updateNs)/float64(base.updateNs))
	}
}
