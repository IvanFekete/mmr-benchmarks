// Package oracle is an independent brute-force ground truth for
// correctness testing. It deliberately shares no code with any candidate
// — using sortedarr as oracle would mean a bug in sort logic would slip
// past every test silently.
//
// Speed is irrelevant; clarity is everything.
package oracle

// Oracle holds player MMRs as an unordered map and computes everything
// via linear scan on demand.
type Oracle struct {
	mmrOf map[uint64]int
}

func New() *Oracle {
	return &Oracle{mmrOf: make(map[uint64]int)}
}

func (o *Oracle) Add(playerID uint64, mmr int) {
	o.mmrOf[playerID] = mmr
}

func (o *Oracle) Remove(playerID uint64) int {
	mmr := o.mmrOf[playerID]
	delete(o.mmrOf, playerID)
	return mmr
}

// Rank: count of players with strictly smaller MMR.
func (o *Oracle) Rank(mmr int) int {
	n := 0
	for _, m := range o.mmrOf {
		if m < mmr {
			n++
		}
	}
	return n
}

// RangeCount: count of players in [lo, hi] inclusive.
func (o *Oracle) RangeCount(lo, hi int) int {
	n := 0
	for _, m := range o.mmrOf {
		if m >= lo && m <= hi {
			n++
		}
	}
	return n
}

func (o *Oracle) Size() int { return len(o.mmrOf) }

func (o *Oracle) Name() string { return "oracle" }
