// Package bench implements the load generator behind cmd/inferbench.
package bench

import (
	"math"
	"sort"
	"time"
)

// Summary describes a latency distribution in milliseconds.
type Summary struct {
	Count  int     `json:"count"`
	MeanMS float64 `json:"mean_ms"`
	MinMS  float64 `json:"min_ms"`
	MaxMS  float64 `json:"max_ms"`
	P50MS  float64 `json:"p50_ms"`
	P95MS  float64 `json:"p95_ms"`
	P99MS  float64 `json:"p99_ms"`
}

// Summarize computes exact (not sampled or approximated) statistics. It uses the
// nearest-rank percentile definition: the smallest value with at least p% of
// observations at or below it.
func Summarize(ds []time.Duration) Summary {
	if len(ds) == 0 {
		return Summary{}
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}
	return Summary{
		Count:  len(sorted),
		MeanMS: ms(sum) / float64(len(sorted)),
		MinMS:  ms(sorted[0]),
		MaxMS:  ms(sorted[len(sorted)-1]),
		P50MS:  ms(percentile(sorted, 50)),
		P95MS:  ms(percentile(sorted, 95)),
		P99MS:  ms(percentile(sorted, 99)),
	}
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = max(1, min(rank, len(sorted)))
	return sorted[rank-1]
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
