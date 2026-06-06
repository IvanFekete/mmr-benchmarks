// Package fenwick implements the Index interface using a Fenwick tree
// (a.k.a. Binary Indexed Tree, Peter Fenwick, 1994) over bucketized MMR.
//
// Bucket size is 1 — every integer MMR value is its own bucket. With
// MaxMMR = 5000 this means 5000 buckets, ~40 KB for the tree itself
// (int64 per bucket). The tree stores counts, not players: tree[i] is
// the count in a power-of-two-aligned prefix. Player identity lives in
// the secondary map maintained at the benchmark level.
//
// Operations:
//   - Add(mmr):        O(log B) — propagate +1 upward.
//   - Remove(mmr):     O(log B) — propagate -1 upward.
//   - Rank(mmr):       O(log B) — prefix sum up to bucket mmr-1.
//   - RangeCount(l,h): O(log B) — two prefix sums.
//   - Size():          O(1).
//
// B = number of buckets = MaxMMR. log B ≈ 13. The whole tree fits in
// a couple of L1 cache lines on access path. This is the cache-locality
// argument the benchmark will quantify.
package fenwick

import (
	"mmrbench/pkg/index"
)

type Fenwick struct {
	tree    []int64        // 1-indexed, tree[i] = count in [i-LSB(i)+1, i]
	mmrOf   map[uint64]int // playerID -> current MMR (for Remove)
	size    int
	bucketN int
}

// Compile-time assertion that *Fenwick satisfies index.Index.
var _ index.Index = (*Fenwick)(nil)

func New() *Fenwick {
	return &Fenwick{
		tree:    make([]int64, index.MaxMMR+1), // 1-indexed
		mmrOf:   make(map[uint64]int),
		bucketN: index.MaxMMR,
	}
}

// NewSized pre-sizes the secondary map for known capacity. Saves
// rehashing during cold-start build. Negligible at steady state.
func NewSized(expectedPlayers int) *Fenwick {
	return &Fenwick{
		tree:    make([]int64, index.MaxMMR+1),
		mmrOf:   make(map[uint64]int, expectedPlayers),
		bucketN: index.MaxMMR,
	}
}

func (f *Fenwick) Name() string { return "fenwick" }

func (f *Fenwick) Add(playerID uint64, mmr int) {
	f.mmrOf[playerID] = mmr
	f.update(mmr+1, +1) // shift to 1-indexed
	f.size++
}

func (f *Fenwick) Remove(playerID uint64) int {
	mmr := f.mmrOf[playerID]
	delete(f.mmrOf, playerID)
	f.update(mmr+1, -1)
	f.size--
	return mmr
}

// update adds delta to bucket idx (1-indexed) and propagates upward.
// Idiomatic Fenwick: keep adding the lowest set bit.
func (f *Fenwick) update(idx int, delta int64) {
	for ; idx <= f.bucketN; idx += idx & -idx {
		f.tree[idx] += delta
	}
}

// prefixSum returns the count in buckets [1..idx] (1-indexed).
// Idiomatic Fenwick: keep clearing the lowest set bit.
func (f *Fenwick) prefixSum(idx int) int64 {
	if idx <= 0 {
		return 0
	}
	if idx > f.bucketN {
		idx = f.bucketN
	}
	var sum int64
	for ; idx > 0; idx -= idx & -idx {
		sum += f.tree[idx]
	}
	return sum
}

// Rank returns the count of players with MMR strictly less than `mmr`.
// MMR value v lives in bucket v+1 (1-indexed). So "strictly less than mmr"
// means prefix sum up to bucket mmr (which covers values 0..mmr-1).
func (f *Fenwick) Rank(mmr int) int {
	if mmr <= 0 {
		return 0
	}
	if mmr > index.MaxMMR {
		mmr = index.MaxMMR
	}
	return int(f.prefixSum(mmr))
}

// RangeCount returns count in [lo, hi] inclusive.
// Maps to prefix(hi+1) - prefix(lo).
func (f *Fenwick) RangeCount(lo, hi int) int {
	if lo < 0 {
		lo = 0
	}
	if hi >= index.MaxMMR {
		hi = index.MaxMMR - 1
	}
	if lo > hi {
		return 0
	}
	return int(f.prefixSum(hi+1) - f.prefixSum(lo))
}

func (f *Fenwick) Size() int { return f.size }

// MemBytes reports just the Fenwick array; the secondary map is reported
// at the benchmark harness level since all candidates need it.
func (f *Fenwick) MemBytes() uint64 {
	return uint64(len(f.tree)) * 8 // int64 per slot
}
