// Package main runs correctness gates for all index candidates against
// the oracle. Run via: go test ./cmd/correctness/
package main

import (
	"fmt"
	"math/rand"
	"os"

	"mmrbench/pkg/index"
	"mmrbench/pkg/index/fenwick"
	"mmrbench/pkg/index/hybrid"
	"mmrbench/pkg/index/skiplist"
	"mmrbench/pkg/index/sortedarr"
	"mmrbench/pkg/oracle"
	"mmrbench/pkg/workload"
)

// op is a single operation in a random trace.
type op struct {
	kind  byte // 'A' add, 'R' remove, 'K' rank query, 'C' range count
	pid   uint64
	mmr   int
	lo    int
	hi    int
}

// generateTrace builds a random operation trace using one distribution.
// Operations are dependent — Remove only picks from currently-present
// players. This matches real workload structure.
func generateTrace(dist workload.Distribution, nOps int, seed int64) []op {
	rng := rand.New(rand.NewSource(seed))
	gen := workload.New(dist, seed+1)
	present := make([]uint64, 0, nOps)
	presentSet := make(map[uint64]bool)
	var nextID uint64 = 1
	ops := make([]op, 0, nOps)

	for len(ops) < nOps {
		// Mix: 30% add, 10% remove (only if non-empty), 20% rank, 40% range.
		// Roughly matches our production mix but biased toward writes early
		// to keep the structure non-trivial.
		r := rng.Float64()
		switch {
		case r < 0.30 || len(present) == 0:
			pid := nextID
			nextID++
			ops = append(ops, op{kind: 'A', pid: pid, mmr: gen.SampleMMR()})
			present = append(present, pid)
			presentSet[pid] = true
		case r < 0.40:
			// Remove a random present player.
			i := rng.Intn(len(present))
			pid := present[i]
			present[i] = present[len(present)-1]
			present = present[:len(present)-1]
			delete(presentSet, pid)
			ops = append(ops, op{kind: 'R', pid: pid})
		case r < 0.60:
			ops = append(ops, op{kind: 'K', mmr: gen.SampleMMR()})
		default:
			lo, hi := gen.SampleRange()
			ops = append(ops, op{kind: 'C', lo: lo, hi: hi})
		}
	}
	return ops
}

// runTrace executes ops on cand and oracle in lockstep, comparing query
// answers. Returns mismatches.
func runTrace(name string, cand index.Index, ops []op) []string {
	orc := oracle.New()
	var mismatches []string
	for i, o := range ops {
		switch o.kind {
		case 'A':
			cand.Add(o.pid, o.mmr)
			orc.Add(o.pid, o.mmr)
		case 'R':
			gotCand := cand.Remove(o.pid)
			gotOrc := orc.Remove(o.pid)
			if gotCand != gotOrc {
				mismatches = append(mismatches,
					fmt.Sprintf("[op %d %s] Remove(%d): got %d, oracle %d", i, name, o.pid, gotCand, gotOrc))
			}
		case 'K':
			gotCand := cand.Rank(o.mmr)
			gotOrc := orc.Rank(o.mmr)
			if gotCand != gotOrc {
				mismatches = append(mismatches,
					fmt.Sprintf("[op %d %s] Rank(%d): got %d, oracle %d (size=%d)", i, name, o.mmr, gotCand, gotOrc, cand.Size()))
			}
		case 'C':
			gotCand := cand.RangeCount(o.lo, o.hi)
			gotOrc := orc.RangeCount(o.lo, o.hi)
			if gotCand != gotOrc {
				mismatches = append(mismatches,
					fmt.Sprintf("[op %d %s] RangeCount(%d,%d): got %d, oracle %d", i, name, o.lo, o.hi, gotCand, gotOrc))
			}
		}
		// Size invariant on every op.
		if cand.Size() != orc.Size() {
			mismatches = append(mismatches,
				fmt.Sprintf("[op %d %s] Size mismatch: cand=%d oracle=%d", i, name, cand.Size(), orc.Size()))
			return mismatches // bail early, structure is desynced
		}
	}
	return mismatches
}

// edgeCases hits specific boundary conditions: empty, single, all-same,
// boundary MMR values.
func edgeCases(name string, newCand func() index.Index) []string {
	var ms []string
	check := func(label string, gotCand, want int) {
		if gotCand != want {
			ms = append(ms, fmt.Sprintf("[edge %s/%s] got %d want %d", name, label, gotCand, want))
		}
	}

	// Empty structure.
	c := newCand()
	check("empty/Size", c.Size(), 0)
	check("empty/Rank(0)", c.Rank(0), 0)
	check("empty/Rank(2500)", c.Rank(2500), 0)
	check("empty/RangeCount", c.RangeCount(0, index.MaxMMR-1), 0)

	// Single element.
	c = newCand()
	c.Add(1, 1500)
	check("single/Size", c.Size(), 1)
	check("single/Rank(1500)", c.Rank(1500), 0) // strictly less
	check("single/Rank(1501)", c.Rank(1501), 1)
	check("single/Range[1500,1500]", c.RangeCount(1500, 1500), 1)
	check("single/Range[0,1499]", c.RangeCount(0, 1499), 0)

	// All-same MMR.
	c = newCand()
	for i := 1; i <= 100; i++ {
		c.Add(uint64(i), 1800)
	}
	check("allsame/Rank(1800)", c.Rank(1800), 0)
	check("allsame/Rank(1801)", c.Rank(1801), 100)
	check("allsame/Range[1800,1800]", c.RangeCount(1800, 1800), 100)

	// MMR boundaries.
	c = newCand()
	c.Add(1, 0)
	c.Add(2, index.MaxMMR-1)
	check("bounds/Rank(0)", c.Rank(0), 0)
	check("bounds/Rank(1)", c.Rank(1), 1)
	check("bounds/Range[0,0]", c.RangeCount(0, 0), 1)
	check("bounds/Range[MaxMMR-1,MaxMMR-1]", c.RangeCount(index.MaxMMR-1, index.MaxMMR-1), 1)
	check("bounds/Range[0,MaxMMR-1]", c.RangeCount(0, index.MaxMMR-1), 2)

	// Update that stays in same bucket (no-op for Fenwick since bucket=1
	// and MMR is integer — but still tests Remove+Add path).
	c = newCand()
	c.Add(1, 1500)
	c.Remove(1)
	c.Add(1, 1500)
	check("readd/Rank(1500)", c.Rank(1500), 0)
	check("readd/Rank(1501)", c.Rank(1501), 1)

	return ms
}

func main() {
	candidates := []struct {
		name string
		make func() index.Index
	}{
		{"fenwick", func() index.Index { return fenwick.New() }},
		{"sortedarr", func() index.Index { return sortedarr.New() }},
		{"skiplist", func() index.Index { return skiplist.New() }},
		{"skiplist-pooled", func() index.Index { return skiplist.NewPooled() }},
		{"hybrid", func() index.Index { return hybrid.New() }},
	}

	dists := []workload.Distribution{workload.Uniform, workload.Skewed, workload.HotRange}
	traceSize := 50_000 // 50K ops per (candidate, distribution)
	seed := int64(42)

	totalFail := 0
	for _, c := range candidates {
		fmt.Printf("=== %s ===\n", c.name)

		// Edge cases first.
		edgeMs := edgeCases(c.name, c.make)
		if len(edgeMs) > 0 {
			totalFail += len(edgeMs)
			for _, m := range edgeMs {
				fmt.Println("  FAIL:", m)
			}
		} else {
			fmt.Println("  edge cases: PASS")
		}

		// Random traces per distribution.
		for _, d := range dists {
			ops := generateTrace(d, traceSize, seed)
			ms := runTrace(c.name, c.make(), ops)
			if len(ms) > 0 {
				totalFail += len(ms)
				fmt.Printf("  trace[%s]: FAIL (%d mismatches)\n", d, len(ms))
				for i, m := range ms {
					if i >= 5 {
						fmt.Printf("    ... and %d more\n", len(ms)-5)
						break
					}
					fmt.Println("   ", m)
				}
			} else {
				fmt.Printf("  trace[%s]: PASS (%d ops)\n", d, traceSize)
			}
		}
	}

	if totalFail > 0 {
		fmt.Printf("\nTOTAL FAILURES: %d\n", totalFail)
		os.Exit(1)
	}
	fmt.Println("\nAll correctness gates PASSED.")
}
