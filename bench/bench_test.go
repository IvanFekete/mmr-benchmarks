package bench

import (
	"math/rand"
	"testing"

	"mmrbench/pkg/index"
	"mmrbench/pkg/index/fenwick"
	"mmrbench/pkg/index/hybrid"
	"mmrbench/pkg/index/skiplist"
	"mmrbench/pkg/index/sortedarr"
	"mmrbench/pkg/workload"
)

// Steady-state size for in-env preview. Pick small enough that sortedarr
// Add (O(N)) finishes in reasonable wall clock — sortedarr at N=100K and
// 1 CPU will still be slow per op, but we want apples-to-apples.
const steadyN = 100_000

type makeFn func() index.Index

var candidates = []struct {
	name string
	make makeFn
}{
	{"fenwick", func() index.Index { return fenwick.NewSized(steadyN) }},
	{"sortedarr", func() index.Index { return sortedarr.NewSized(steadyN) }},
	{"skiplist", func() index.Index { return skiplist.NewSized(steadyN) }},
	{"skiplist-pooled", func() index.Index { return skiplist.NewPooledSized(steadyN) }},
	{"hybrid", func() index.Index { return hybrid.NewSized(steadyN) }},
}

var dists = []workload.Distribution{
	workload.Uniform,
	workload.Skewed,
	workload.HotRange,
}

// prefill loads idx with steadyN players from the given distribution and
// returns the player IDs and the MMRs (so callers can know what's in there).
func prefill(idx index.Index, dist workload.Distribution, seed int64) ([]uint64, []int) {
	gen := workload.New(dist, seed)
	ids := make([]uint64, steadyN)
	mmrs := make([]int, steadyN)
	for i := 0; i < steadyN; i++ {
		ids[i] = uint64(i + 1)
		mmrs[i] = gen.SampleMMR()
		idx.Add(ids[i], mmrs[i])
	}
	return ids, mmrs
}

// BenchmarkRank: 100% rank queries against a steady-state index.
func BenchmarkRank(b *testing.B) {
	for _, c := range candidates {
		for _, d := range dists {
			b.Run(c.name+"/"+d.String(), func(b *testing.B) {
				idx := c.make()
				prefill(idx, d, 1)
				queryGen := workload.New(d, 999)
				queries := make([]int, b.N)
				for i := range queries {
					queries[i] = queryGen.SampleMMR()
				}
				b.ResetTimer()
				var sink int
				for i := 0; i < b.N; i++ {
					sink += idx.Rank(queries[i])
				}
				_ = sink
			})
		}
	}
}

// BenchmarkRangeCount: 100% range queries against steady state.
func BenchmarkRangeCount(b *testing.B) {
	for _, c := range candidates {
		for _, d := range dists {
			b.Run(c.name+"/"+d.String(), func(b *testing.B) {
				idx := c.make()
				prefill(idx, d, 1)
				queryGen := workload.New(d, 999)
				ranges := make([][2]int, b.N)
				for i := range ranges {
					lo, hi := queryGen.SampleRange()
					ranges[i] = [2]int{lo, hi}
				}
				b.ResetTimer()
				var sink int
				for i := 0; i < b.N; i++ {
					sink += idx.RangeCount(ranges[i][0], ranges[i][1])
				}
				_ = sink
			})
		}
	}
}

// BenchmarkUpdate: simulates the "remove old + add new MMR" pair (b-semantics
// we locked in design). One bench iteration = one full update cycle.
func BenchmarkUpdate(b *testing.B) {
	for _, c := range candidates {
		for _, d := range dists {
			b.Run(c.name+"/"+d.String(), func(b *testing.B) {
				idx := c.make()
				_, _ = prefill(idx, d, 1)
				// Track currently-present players by re-deriving a slice.
				// Cheaper than reusing prefill ids since they're stable.
				present := make([]uint64, steadyN)
				for i := range present {
					present[i] = uint64(i + 1)
				}
				rng := rand.New(rand.NewSource(2))
				gen := workload.New(d, 7)
				// Pre-generate the ops to avoid measuring rand/gen time.
				type updOp struct {
					removeIdx int
					newMMR    int
					newID     uint64
				}
				ops := make([]updOp, b.N)
				nextID := uint64(steadyN + 1)
				for i := range ops {
					ops[i] = updOp{
						removeIdx: rng.Intn(steadyN),
						newMMR:    gen.SampleMMR(),
						newID:     nextID,
					}
					nextID++
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					op := ops[i]
					victim := present[op.removeIdx]
					idx.Remove(victim)
					idx.Add(op.newID, op.newMMR)
					present[op.removeIdx] = op.newID
				}
			})
		}
	}
}
