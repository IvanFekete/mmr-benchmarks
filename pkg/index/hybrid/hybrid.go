// Package hybrid combines a Fenwick tree (for O(log B) count/rank/range
// aggregates) with per-bucket slices of player IDs (for O(K) enumeration
// of who actually sits in a given MMR range).
//
// Architecture sketch:
//
//   tree     []int64        // Fenwick tree, exactly like the standalone Fenwick
//   buckets  [][]uint64     // buckets[mmr] = player IDs currently at that MMR
//   info     map[uint64]playerInfo  // playerID -> (mmr, indexInBucket)
//
// Add appends to the bucket and updates the tree. Remove uses swap-and-pop
// on the bucket (O(1) with bookkeeping) and updates the tree.
//
// Trade-off vs pure Fenwick:
//   + Adds Enumerate(lo, hi) -> []playerID, O(K) where K = result size.
//     This is the matchmaker fine-pass primitive that pure Fenwick can't do.
//   - ~30% more memory at steady state (info entries +8B, bucket data
//     +8B per player).
//   - Slightly slower Add/Remove than pure Fenwick (extra slice manipulation
//     and a second map entry).
//
// Rank and RangeCount delegate to the Fenwick part — identical cost to
// the standalone Fenwick candidate. That's the whole pitch.
package hybrid

import (
	"mmrbench/pkg/index"
)

type playerInfo struct {
	mmr int32
	pos int32 // index into buckets[mmr]
}

type Hybrid struct {
	tree    []int64
	buckets [][]uint64
	info    map[uint64]playerInfo
	size    int
	bucketN int
}

var _ index.Index = (*Hybrid)(nil)

func New() *Hybrid {
	return &Hybrid{
		tree:    make([]int64, index.MaxMMR+1),
		buckets: make([][]uint64, index.MaxMMR),
		info:    make(map[uint64]playerInfo),
		bucketN: index.MaxMMR,
	}
}

func NewSized(expectedPlayers int) *Hybrid {
	h := &Hybrid{
		tree:    make([]int64, index.MaxMMR+1),
		buckets: make([][]uint64, index.MaxMMR),
		info:    make(map[uint64]playerInfo, expectedPlayers),
		bucketN: index.MaxMMR,
	}
	// Optional: pre-size buckets to expected density. We don't, because
	// (a) the article will discuss this as a tunable, and (b) Go slice
	// growth is fast enough at typical bucket sizes.
	return h
}

func (h *Hybrid) Name() string { return "hybrid" }

func (h *Hybrid) Add(playerID uint64, mmr int) {
	pos := int32(len(h.buckets[mmr]))
	h.buckets[mmr] = append(h.buckets[mmr], playerID)
	h.info[playerID] = playerInfo{mmr: int32(mmr), pos: pos}

	// Fenwick: +1 at bucket mmr+1 (1-indexed shift).
	for idx := mmr + 1; idx <= h.bucketN; idx += idx & -idx {
		h.tree[idx]++
	}
	h.size++
}

func (h *Hybrid) Remove(playerID uint64) int {
	pi := h.info[playerID]
	delete(h.info, playerID)

	// Swap-and-pop from buckets[pi.mmr].
	bucket := h.buckets[pi.mmr]
	lastIdx := int32(len(bucket) - 1)
	if pi.pos != lastIdx {
		moved := bucket[lastIdx]
		bucket[pi.pos] = moved
		mi := h.info[moved]
		mi.pos = pi.pos
		h.info[moved] = mi
	}
	h.buckets[pi.mmr] = bucket[:lastIdx]

	// Fenwick: -1.
	mmr := int(pi.mmr)
	for idx := mmr + 1; idx <= h.bucketN; idx += idx & -idx {
		h.tree[idx]--
	}
	h.size--
	return mmr
}

// prefixSum is identical to fenwick.prefixSum.
func (h *Hybrid) prefixSum(idx int) int64 {
	if idx <= 0 {
		return 0
	}
	if idx > h.bucketN {
		idx = h.bucketN
	}
	var sum int64
	for ; idx > 0; idx -= idx & -idx {
		sum += h.tree[idx]
	}
	return sum
}

func (h *Hybrid) Rank(mmr int) int {
	if mmr <= 0 {
		return 0
	}
	if mmr > index.MaxMMR {
		mmr = index.MaxMMR
	}
	return int(h.prefixSum(mmr))
}

func (h *Hybrid) RangeCount(lo, hi int) int {
	if lo < 0 {
		lo = 0
	}
	if hi >= index.MaxMMR {
		hi = index.MaxMMR - 1
	}
	if lo > hi {
		return 0
	}
	return int(h.prefixSum(hi+1) - h.prefixSum(lo))
}

// Enumerate returns all player IDs with MMR in [lo, hi] inclusive.
// O(K + (hi-lo)) — K result IDs, plus walk over each bucket in range.
// This is the primitive pure Fenwick can't provide and is the whole
// reason hybrid exists. Not part of the Index interface; callers wanting
// enumeration use Hybrid directly.
func (h *Hybrid) Enumerate(lo, hi int, out []uint64) []uint64 {
	if lo < 0 {
		lo = 0
	}
	if hi >= index.MaxMMR {
		hi = index.MaxMMR - 1
	}
	for mmr := lo; mmr <= hi; mmr++ {
		out = append(out, h.buckets[mmr]...)
	}
	return out
}

func (h *Hybrid) Size() int { return h.size }

func (h *Hybrid) MemBytes() uint64 {
	// Fenwick tree: 8 bytes per slot.
	treeBytes := uint64(len(h.tree)) * 8
	// Bucket slice headers: 24 bytes per bucket * bucketN buckets.
	headerBytes := uint64(h.bucketN) * 24
	// Bucket contents: 8 bytes per player ID, summed across all buckets.
	var contentBytes uint64
	for _, b := range h.buckets {
		contentBytes += uint64(cap(b)) * 8
	}
	return treeBytes + headerBytes + contentBytes
}
