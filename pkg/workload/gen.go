// Package workload generates MMR samples from three distributions
// (uniform, realistic-skewed, hot-range) with deterministic seeding.
//
// Distribution parameters are locked from the benchmark design discussion
// and intentionally not configurable at the CLI level — keeping them
// hardcoded means every reader running the bench gets the same numbers,
// not a configuration zoo.
package workload

import (
	"math"
	"math/rand"

	"mmrbench/pkg/index"
)

type Distribution int

const (
	Uniform Distribution = iota
	Skewed
	HotRange
)

func (d Distribution) String() string {
	switch d {
	case Uniform:
		return "uniform"
	case Skewed:
		return "skewed"
	case HotRange:
		return "hot_range"
	}
	return "unknown"
}

// Generator produces MMR samples and player IDs.
type Generator struct {
	rng  *rand.Rand
	dist Distribution
}

func New(dist Distribution, seed int64) *Generator {
	return &Generator{
		rng:  rand.New(rand.NewSource(seed)),
		dist: dist,
	}
}

// SampleMMR returns one MMR value clipped to [0, MaxMMR).
func (g *Generator) SampleMMR() int {
	var v float64
	switch g.dist {
	case Uniform:
		v = g.rng.Float64() * float64(index.MaxMMR)

	case Skewed:
		// Gaussian mixture: bronze/silver-gold/platinum/diamond clusters.
		// Weights chosen to roughly mirror publicly published LoL rank
		// distribution. See article references section.
		u := g.rng.Float64()
		var mu, sigma float64
		switch {
		case u < 0.15:
			mu, sigma = 1000, 80
		case u < 0.65:
			mu, sigma = 1500, 120
		case u < 0.90:
			mu, sigma = 2000, 100
		default:
			mu, sigma = 2500, 150
		}
		v = g.rng.NormFloat64()*sigma + mu

	case HotRange:
		// 80% tight cluster around median, 20% spread thin.
		u := g.rng.Float64()
		if u < 0.80 {
			v = g.rng.NormFloat64()*60 + 1500
		} else {
			v = g.rng.Float64()*3800 + 200 // U[200, 4000]
		}
	}

	// Clip and quantize to integer MMR (matching production rating systems).
	iv := int(math.Round(v))
	if iv < 0 {
		iv = 0
	}
	if iv >= index.MaxMMR {
		iv = index.MaxMMR - 1
	}
	return iv
}

// SampleRange returns a (lo, hi) pair appropriate for matchmaker
// adaptive-radius queries. Radius chosen log-uniform from [25, 500]
// matching the typical ±50 → ±500 expansion arc.
func (g *Generator) SampleRange() (int, int) {
	center := g.SampleMMR()
	// Log-uniform radius in [25, 500].
	radius := int(math.Round(math.Exp(g.rng.Float64()*math.Log(500.0/25.0)) * 25))
	lo := center - radius
	hi := center + radius
	if lo < 0 {
		lo = 0
	}
	if hi >= index.MaxMMR {
		hi = index.MaxMMR - 1
	}
	return lo, hi
}

// PopulateInto fills an Index with n players. Returns the player IDs
// in insertion order so the caller can use them for Remove later.
func (g *Generator) PopulateInto(idx index.Index, n int) []uint64 {
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		ids[i] = uint64(i + 1) // 1-indexed, 0 reserved for "no player"
		idx.Add(ids[i], g.SampleMMR())
	}
	return ids
}
