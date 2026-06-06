// Package measure provides latency capture utilities tuned for
// ns-scale benchmarking. We deliberately avoid the HDR-histogram
// dependency: at our sample volumes (under ~10M per op kind),
// sort-based exact percentiles are simpler and fully precise.
package measure

import (
	"sort"
	"time"
)

// Recorder captures per-op latency samples for one operation kind.
// Pre-size with NewRecorder(n) when the expected sample count is known
// to avoid slice growth during measurement.
type Recorder struct {
	samples []int64 // nanoseconds
}

func NewRecorder(capacity int) *Recorder {
	return &Recorder{samples: make([]int64, 0, capacity)}
}

func (r *Recorder) Record(ns int64) {
	r.samples = append(r.samples, ns)
}

func (r *Recorder) Count() int { return len(r.samples) }

// Percentiles returns p50, p90, p99, p99.9, max — in nanoseconds.
// Sorts the underlying slice in-place. Call only after all samples
// are recorded.
func (r *Recorder) Percentiles() (p50, p90, p99, p999, max int64) {
	n := len(r.samples)
	if n == 0 {
		return 0, 0, 0, 0, 0
	}
	sort.Slice(r.samples, func(i, j int) bool { return r.samples[i] < r.samples[j] })
	pick := func(q float64) int64 {
		idx := int(q * float64(n-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return r.samples[idx]
	}
	p50 = pick(0.50)
	p90 = pick(0.90)
	p99 = pick(0.99)
	p999 = pick(0.999)
	max = r.samples[n-1]
	return
}

// Mean returns the arithmetic mean of all samples in nanoseconds.
// Useful as a sanity check against testing.B numbers.
func (r *Recorder) Mean() float64 {
	if len(r.samples) == 0 {
		return 0
	}
	var sum int64
	for _, s := range r.samples {
		sum += s
	}
	return float64(sum) / float64(len(r.samples))
}

// CalibrateTimerOverhead measures the cost of paired time.Now() +
// time.Since() calls on this machine. Returns median over n iterations.
// Subtract this from per-op measurements when reporting absolute ns/op
// of sub-200ns operations, OR (preferred for honesty) report it
// alongside the raw numbers.
func CalibrateTimerOverhead(n int) int64 {
	samples := make([]int64, n)
	// Warm up.
	for i := 0; i < 1000; i++ {
		_ = time.Since(time.Now())
	}
	for i := 0; i < n; i++ {
		start := time.Now()
		samples[i] = time.Since(start).Nanoseconds()
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[n/2] // median
}
