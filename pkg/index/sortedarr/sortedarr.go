// Package sortedarr implements Index as a sorted slice of (MMR, playerID)
// pairs with binary search. This is the "what you'd write in 20 minutes
// if you just opened the editor" baseline. O(log N) queries, O(N) updates
// because we maintain sort order on every Add/Remove.
//
// Inclusion rationale: the article needs an honest "from what we optimize"
// reference. If Fenwick is only 2x faster than this on the hot path, the
// engineering investment isn't justified.
package sortedarr

import (
	"sort"

	"mmrbench/pkg/index"
)

type entry struct {
	mmr      int32
	playerID uint64
}

type SortedArr struct {
	data  []entry
	mmrOf map[uint64]int
}

var _ index.Index = (*SortedArr)(nil)

func New() *SortedArr {
	return &SortedArr{
		mmrOf: make(map[uint64]int),
	}
}

func NewSized(expectedPlayers int) *SortedArr {
	return &SortedArr{
		data:  make([]entry, 0, expectedPlayers),
		mmrOf: make(map[uint64]int, expectedPlayers),
	}
}

func (s *SortedArr) Name() string { return "sortedarr" }

// findInsertIdx returns the index where (mmr, playerID) would be inserted
// to keep order. Sort key is (mmr, playerID) — playerID breaks ties so
// removal is unambiguous.
func (s *SortedArr) findInsertIdx(mmr int, playerID uint64) int {
	return sort.Search(len(s.data), func(i int) bool {
		e := s.data[i]
		if int(e.mmr) != mmr {
			return int(e.mmr) > mmr
		}
		return e.playerID >= playerID
	})
}

func (s *SortedArr) Add(playerID uint64, mmr int) {
	s.mmrOf[playerID] = mmr
	idx := s.findInsertIdx(mmr, playerID)
	s.data = append(s.data, entry{}) // grow
	copy(s.data[idx+1:], s.data[idx:])
	s.data[idx] = entry{mmr: int32(mmr), playerID: playerID}
}

func (s *SortedArr) Remove(playerID uint64) int {
	mmr := s.mmrOf[playerID]
	delete(s.mmrOf, playerID)
	idx := s.findInsertIdx(mmr, playerID)
	s.data = append(s.data[:idx], s.data[idx+1:]...)
	return mmr
}

// Rank: count of entries with mmr < given mmr. Binary search for the
// first entry whose mmr >= the query.
func (s *SortedArr) Rank(mmr int) int {
	if mmr <= 0 {
		return 0
	}
	return sort.Search(len(s.data), func(i int) bool {
		return int(s.data[i].mmr) >= mmr
	})
}

// RangeCount: rank(hi+1) - rank(lo).
func (s *SortedArr) RangeCount(lo, hi int) int {
	if lo > hi {
		return 0
	}
	lowIdx := sort.Search(len(s.data), func(i int) bool {
		return int(s.data[i].mmr) >= lo
	})
	highIdx := sort.Search(len(s.data), func(i int) bool {
		return int(s.data[i].mmr) > hi
	})
	return highIdx - lowIdx
}

func (s *SortedArr) Size() int { return len(s.data) }

func (s *SortedArr) MemBytes() uint64 {
	// 12 bytes per entry (int32 mmr + uint64 playerID, no padding due to ordering)
	// Go alignment will pad to 16 bytes. Be honest: report cap*16.
	return uint64(cap(s.data)) * 16
}
