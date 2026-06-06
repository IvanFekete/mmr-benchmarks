// Command percentile runs a sustained mixed workload at the production
// op mix (1K updates : 5K rank : 50K range count per second target,
// which is 1.78% : 8.93% : 89.29%) and reports per-op-kind latency
// percentiles plus GC and throughput stats.
//
// This is scenario B from our benchmark design discussion.
//
// In-env, we run for a fixed sample count rather than wall clock time
// because the 1-CPU host can't sustain the production rate target —
// the bench would just become CPU-bound. What we measure instead is
// the latency distribution at "as fast as the box can go", which is
// still informative for comparison across candidates.
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
	"mmrbench/pkg/measure"
	"mmrbench/pkg/workload"
)

const (
	steadyN   = 100_000
	totalOps  = 500_000 // total ops across the mixed workload
	updatePct = 0.0178  // 1K of 56K
	rankPct   = 0.0893  // 5K of 56K
	// rangePct = 0.8929 — the rest
)

type opKind int

const (
	opUpdate opKind = iota
	opRank
	opRange
)

func (o opKind) String() string {
	switch o {
	case opUpdate:
		return "Update"
	case opRank:
		return "Rank"
	case opRange:
		return "Range"
	}
	return "?"
}

type result struct {
	candidate string
	dist      string
	op        opKind
	rec       *measure.Recorder
	totalNs   int64
	gcCount   uint32
	gcPauseNs uint64
}

// runMixed runs a mixed workload of totalOps operations on the given
// candidate prefilled with steadyN players from dist. Returns
// percentile recorders per op kind, plus throughput and GC stats.
func runMixed(idx index.Index, dist workload.Distribution, name string) []result {
	gen := workload.New(dist, 42)
	// Prefill.
	present := make([]uint64, steadyN)
	for i := 0; i < steadyN; i++ {
		present[i] = uint64(i + 1)
		idx.Add(present[i], gen.SampleMMR())
	}
	nextID := uint64(steadyN + 1)

	// Pre-generate ops to keep workload deterministic and avoid mixing
	// rand cost into per-op latency.
	type plannedOp struct {
		kind    opKind
		victim  int    // index into `present` for updates
		mmrLo   int    // for rank: mmr; for range: lo
		mmrHi   int    // for range: hi
		newMMR  int    // for update
		newID   uint64 // for update
	}
	ops := make([]plannedOp, totalOps)
	rng := rand.New(rand.NewSource(7))
	for i := range ops {
		u := rng.Float64()
		switch {
		case u < updatePct:
			ops[i] = plannedOp{
				kind:   opUpdate,
				victim: rng.Intn(steadyN),
				newMMR: gen.SampleMMR(),
				newID:  nextID,
			}
			nextID++
		case u < updatePct+rankPct:
			ops[i] = plannedOp{kind: opRank, mmrLo: gen.SampleMMR()}
		default:
			lo, hi := gen.SampleRange()
			ops[i] = plannedOp{kind: opRange, mmrLo: lo, mmrHi: hi}
		}
	}

	// Pre-size recorders.
	updateCount := 0
	rankCount := 0
	rangeCount := 0
	for _, op := range ops {
		switch op.kind {
		case opUpdate:
			updateCount++
		case opRank:
			rankCount++
		case opRange:
			rangeCount++
		}
	}
	recU := measure.NewRecorder(updateCount)
	recK := measure.NewRecorder(rankCount)
	recC := measure.NewRecorder(rangeCount)

	// Capture GC baseline.
	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Sink to defeat dead-code elimination on Rank/Range results.
	var sinkI int

	start := time.Now()
	for i := range ops {
		op := ops[i]
		t0 := time.Now()
		switch op.kind {
		case opUpdate:
			victim := present[op.victim]
			idx.Remove(victim)
			idx.Add(op.newID, op.newMMR)
			present[op.victim] = op.newID
			recU.Record(time.Since(t0).Nanoseconds())
		case opRank:
			sinkI += idx.Rank(op.mmrLo)
			recK.Record(time.Since(t0).Nanoseconds())
		case opRange:
			sinkI += idx.RangeCount(op.mmrLo, op.mmrHi)
			recC.Record(time.Since(t0).Nanoseconds())
		}
	}
	totalElapsed := time.Since(start)
	_ = sinkI

	runtime.ReadMemStats(&mAfter)
	gcDelta := mAfter.NumGC - mBefore.NumGC
	pauseDelta := mAfter.PauseTotalNs - mBefore.PauseTotalNs

	mkResult := func(op opKind, rec *measure.Recorder) result {
		return result{
			candidate: name,
			dist:      dist.String(),
			op:        op,
			rec:       rec,
			totalNs:   totalElapsed.Nanoseconds(),
			gcCount:   gcDelta,
			gcPauseNs: pauseDelta,
		}
	}
	return []result{
		mkResult(opUpdate, recU),
		mkResult(opRank, recK),
		mkResult(opRange, recC),
	}
}

func main() {
	// Calibrate timer overhead first.
	overheadNs := measure.CalibrateTimerOverhead(10_000)
	fmt.Printf("Timer overhead (median paired time.Now+time.Since): %d ns\n\n", overheadNs)

	cands := []struct {
		name string
		make func() index.Index
	}{
		{"fenwick", func() index.Index { return fenwick.NewSized(steadyN) }},
		{"hybrid", func() index.Index { return hybrid.NewSized(steadyN) }},
		{"sortedarr", func() index.Index { return sortedarr.NewSized(steadyN) }},
		{"skiplist", func() index.Index { return skiplist.NewSized(steadyN) }},
		{"skiplist-pooled", func() index.Index { return skiplist.NewPooledSized(steadyN) }},
	}
	dists := []workload.Distribution{
		workload.Uniform,
		workload.Skewed,
		workload.HotRange,
	}

	// Skip sortedarr on update-bearing scenarios because it's so slow it
	// would dwarf the run time of everything else combined. We have
	// the isolated update numbers already (~270 µs/op). For the mixed
	// run we still include it but mark it.
	header := fmt.Sprintf("%-18s %-10s %-6s %8s %8s %8s %8s %8s %8s %5s %8s",
		"candidate", "dist", "op", "count", "p50", "p90", "p99", "p99.9", "max", "GCs", "pause(ms)")
	fmt.Println(header)
	fmt.Println("---------------------------------------------------------------------------------------------------------------")

	for _, d := range dists {
		for _, c := range cands {
			if c.name == "sortedarr" {
				// Sortedarr update at N=100K is ~270 µs. 8910 update ops
				// in totalOps would take ~2.4 seconds just for updates.
				// Manageable but inflates wall time; we let it run.
			}
			res := runMixed(c.make(), d, c.name)
			for _, r := range res {
				p50, p90, p99, p999, max := r.rec.Percentiles()
				fmt.Printf("%-18s %-10s %-6s %8d %8d %8d %8d %8d %8d %5d %8.2f\n",
					r.candidate, r.dist, r.op.String(),
					r.rec.Count(), p50, p90, p99, p999, max,
					r.gcCount, float64(r.gcPauseNs)/1e6)
			}
			runtime.GC()
		}
		fmt.Println()
	}
}
