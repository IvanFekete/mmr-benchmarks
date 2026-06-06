// Package skiplist implements Index as a Redis ZSET-style skip list.
//
// The critical detail for matchmaking use is the span field on each
// forward pointer: span[i] = number of level-0 steps from this node to
// forward[i]. Without span, Rank query degenerates to O(N) linear walk,
// killing the whole point of skip list as a leaderboard. With span,
// Rank is O(log N) — traverse top-down, sum spans of taken forward
// pointers.
//
// Two constructors:
//   - New()      — direct allocation per Add. GC sees full pressure.
//   - NewPooled() — sync.Pool per level. Returns nodes to pool on Remove.
//
// Comparing the two lets us isolate "skip list algorithmic cost" from
// "Go allocator overhead", which the article will discuss as a separate
// trade-off axis.
package skiplist

import (
	"math/rand"
	"sync"

	"mmrbench/pkg/index"
)

const (
	maxLevel = 16    // covers up to ~4B elements at p=0.25
	pBranch  = 0.25  // standard Redis probability
)

// level holds a forward pointer and the span (level-0 distance) to it.
// Packed together so each level's metadata is one struct, halving the
// slice-header overhead vs separate forward[] and span[] slices.
type lvl struct {
	forward *node
	span    uint32
}

type node struct {
	mmr      int32
	playerID uint64
	backward *node // for reverse iteration; not strictly required by Index
	levels   []lvl // len = node's level + 1
}

type SkipList struct {
	head     *node
	tail     *node
	length   int
	level    int // current max level index in use (0..maxLevel)
	mmrOf    map[uint64]int
	rng      *rand.Rand
	pooled   bool
	nodePool [maxLevel + 1]*sync.Pool // pool[i] holds nodes whose len(levels) == i+1
}

var _ index.Index = (*SkipList)(nil)

// New returns a SkipList with direct allocation.
func New() *SkipList { return newSkipList(false, 0) }

// NewPooled returns a SkipList that recycles nodes via sync.Pool.
func NewPooled() *SkipList { return newSkipList(true, 0) }

// NewSized pre-sizes the secondary map. Recommended for known load.
func NewSized(expectedPlayers int) *SkipList { return newSkipList(false, expectedPlayers) }

// NewPooledSized combines pooling with pre-sized secondary map.
func NewPooledSized(expectedPlayers int) *SkipList { return newSkipList(true, expectedPlayers) }

func newSkipList(pool bool, expected int) *SkipList {
	sl := &SkipList{
		mmrOf:  make(map[uint64]int, expected),
		rng:    rand.New(rand.NewSource(1)),
		pooled: pool,
	}
	// Head sentinel always has maxLevel+1 forward pointers.
	sl.head = &node{levels: make([]lvl, maxLevel+1)}
	if pool {
		for i := 0; i <= maxLevel; i++ {
			i := i // capture
			sl.nodePool[i] = &sync.Pool{
				New: func() interface{} {
					return &node{levels: make([]lvl, i+1)}
				},
			}
		}
	}
	return sl
}

func (sl *SkipList) Name() string {
	if sl.pooled {
		return "skiplist-pooled"
	}
	return "skiplist"
}

// randomLevel returns a level in [0, maxLevel] following the geometric
// distribution with parameter pBranch. Standard skip list level choice.
func (sl *SkipList) randomLevel() int {
	level := 0
	for sl.rng.Float64() < pBranch && level < maxLevel {
		level++
	}
	return level
}

func (sl *SkipList) newNode(lv int, mmr int, playerID uint64) *node {
	if sl.pooled {
		n := sl.nodePool[lv].Get().(*node)
		n.mmr = int32(mmr)
		n.playerID = playerID
		n.backward = nil
		// Levels slice is right-sized for this pool bucket, but may hold
		// stale pointers/spans from previous occupancy. Zero them.
		for i := range n.levels {
			n.levels[i] = lvl{}
		}
		return n
	}
	return &node{
		mmr:      int32(mmr),
		playerID: playerID,
		levels:   make([]lvl, lv+1),
	}
}

func (sl *SkipList) freeNode(n *node) {
	if !sl.pooled {
		return
	}
	bucket := len(n.levels) - 1
	sl.nodePool[bucket].Put(n)
}

// nodeLess returns true when (a.mmr, a.playerID) sorts strictly before
// (mmr, playerID). Tie-break on playerID makes (score, member) tuples
// unique just like Redis ZSET.
func nodeLess(a *node, mmr int, playerID uint64) bool {
	if int(a.mmr) != mmr {
		return int(a.mmr) < mmr
	}
	return a.playerID < playerID
}

// Add inserts (playerID, mmr). Caller guarantees no duplicate playerID.
func (sl *SkipList) Add(playerID uint64, mmr int) {
	sl.mmrOf[playerID] = mmr

	var update [maxLevel + 1]*node
	var rank [maxLevel + 1]uint32

	x := sl.head
	for i := sl.level; i >= 0; i-- {
		if i == sl.level {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.levels[i].forward != nil && nodeLess(x.levels[i].forward, mmr, playerID) {
			rank[i] += x.levels[i].span
			x = x.levels[i].forward
		}
		update[i] = x
	}

	lv := sl.randomLevel()
	if lv > sl.level {
		// New top levels: head was the predecessor and "spanned" the whole list.
		for i := sl.level + 1; i <= lv; i++ {
			rank[i] = 0
			update[i] = sl.head
			update[i].levels[i].span = uint32(sl.length)
		}
		sl.level = lv
	}

	n := sl.newNode(lv, mmr, playerID)
	for i := 0; i <= lv; i++ {
		n.levels[i].forward = update[i].levels[i].forward
		update[i].levels[i].forward = n
		// Split predecessor's span at insertion point.
		n.levels[i].span = update[i].levels[i].span - (rank[0] - rank[i])
		update[i].levels[i].span = (rank[0] - rank[i]) + 1
	}
	// Levels above the new node's level: their spans grow by 1.
	for i := lv + 1; i <= sl.level; i++ {
		update[i].levels[i].span++
	}

	if update[0] != sl.head {
		n.backward = update[0]
	}
	if n.levels[0].forward != nil {
		n.levels[0].forward.backward = n
	} else {
		sl.tail = n
	}
	sl.length++
}

// Remove deletes (playerID); MMR is looked up from secondary map.
func (sl *SkipList) Remove(playerID uint64) int {
	mmr, ok := sl.mmrOf[playerID]
	if !ok {
		return -1 // contract violation, but don't panic in production
	}
	delete(sl.mmrOf, playerID)

	var update [maxLevel + 1]*node
	x := sl.head
	for i := sl.level; i >= 0; i-- {
		for x.levels[i].forward != nil && nodeLess(x.levels[i].forward, mmr, playerID) {
			x = x.levels[i].forward
		}
		update[i] = x
	}
	x = x.levels[0].forward
	if x == nil || int(x.mmr) != mmr || x.playerID != playerID {
		// Defensive: secondary map said this player exists but list disagrees.
		// Shouldn't happen if Add/Remove preconditions are honored.
		return mmr
	}

	for i := 0; i <= sl.level; i++ {
		if update[i].levels[i].forward == x {
			// We're at a level where the deleted node was directly forward.
			// Stitch: predecessor's span absorbs x's span, minus 1 for x itself.
			update[i].levels[i].span += x.levels[i].span - 1
			update[i].levels[i].forward = x.levels[i].forward
		} else {
			// Higher levels just lose 1 from their span.
			update[i].levels[i].span--
		}
	}

	if x.levels[0].forward != nil {
		x.levels[0].forward.backward = x.backward
	} else {
		sl.tail = x.backward
	}

	// Shrink top empty levels.
	for sl.level > 0 && sl.head.levels[sl.level].forward == nil {
		sl.level--
	}

	sl.length--
	sl.freeNode(x)
	return mmr
}

// Rank returns count of players with MMR strictly less than the given.
// Uses score-only comparison (no playerID tie-break, since we want all
// players at that exact MMR to count as "equal or above").
func (sl *SkipList) Rank(mmr int) int {
	if mmr <= 0 {
		return 0
	}
	x := sl.head
	var rank uint32
	for i := sl.level; i >= 0; i-- {
		for x.levels[i].forward != nil && int(x.levels[i].forward.mmr) < mmr {
			rank += x.levels[i].span
			x = x.levels[i].forward
		}
	}
	return int(rank)
}

// RangeCount [lo, hi] inclusive. Maps to Rank(hi+1) - Rank(lo).
func (sl *SkipList) RangeCount(lo, hi int) int {
	if lo > hi {
		return 0
	}
	if lo < 0 {
		lo = 0
	}
	if hi >= index.MaxMMR {
		hi = index.MaxMMR - 1
	}
	return sl.Rank(hi+1) - sl.Rank(lo)
}

func (sl *SkipList) Size() int { return sl.length }

// MemBytes is a static estimate. Real memory consumed by the structure
// is best measured via runtime.MemStats at the harness level; this just
// gives a back-of-envelope number for quick reporting.
//
// Per node: header (32B) + slice header (24B) + 16B per level.
// Expected levels per node at p=0.25 is 1/(1-p) = 1.33.
// Average bytes per node ≈ 32 + 24 + 16*1.33 ≈ 77 bytes.
// Plus head sentinel: 32 + 24 + 16*17 = 328 bytes.
func (sl *SkipList) MemBytes() uint64 {
	const avgPerNode = 77
	return uint64(sl.length)*avgPerNode + 328
}
