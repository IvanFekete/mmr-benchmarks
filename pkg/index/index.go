// Package index defines the common interface all matchmaking index
// candidates implement. The semantics are deliberately picked to match
// realistic production usage:
//
//   - Players are added when they enter the queue.
//   - When a match completes, a player exits queue (Remove by ID) and
//     re-enters later with a new MMR (Add again). This is the (b) update
//     model we locked in benchmark design — two ops per MMR change, not one.
//   - Rank query is exact: count of players with MMR strictly less than
//     the given value. Ties broken by entry order in production; here we
//     ignore within-tie ordering since it doesn't affect rank for UI.
//   - Range count is inclusive on both bounds.
//   - MMR is an int in a known bounded range. We assume [0, MaxMMR).
package index

// MaxMMR is the exclusive upper bound of the MMR domain. 5000 covers all
// realistic ratings — TrueSkill display values for Halo/Forza stay well
// under, LoL/Dota Elo derivatives top out around 3000, chess FIDE tops
// around 2900. 5000 leaves headroom without inflating Fenwick footprint.
const MaxMMR = 5000

// Index is the contract every candidate satisfies. All operations must
// be safe to call from a single goroutine; concurrency wrappers come at
// a higher layer (per-shard locking is the production pattern).
type Index interface {
	// Add inserts a player with the given MMR. Caller guarantees the
	// player is not currently in the index. MMR must be in [0, MaxMMR).
	Add(playerID uint64, mmr int)

	// Remove deletes the player. Caller guarantees the player exists.
	// Returns the MMR the player had at removal time (useful for the
	// caller to compute deltas in the matchmaker).
	Remove(playerID uint64) int

	// Rank returns the number of players currently in the index with
	// MMR strictly less than the given value. This is the "count below"
	// primitive — rank in a 1-indexed leaderboard is Rank(mmr) + 1, and
	// percentile is Rank(mmr) / Size().
	Rank(mmr int) int

	// RangeCount returns the number of players with MMR in [lo, hi]
	// inclusive. lo and hi are clamped to [0, MaxMMR).
	RangeCount(lo, hi int) int

	// Size returns total players currently in the index.
	Size() int

	// MemBytes returns the structural memory footprint in bytes. This
	// excludes Go runtime overhead and the secondary playerID lookup
	// map (which all candidates need equally — we report it once at
	// the benchmark level).
	MemBytes() uint64

	// Name returns a short identifier for reporting.
	Name() string
}
