package bench

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
)

// ErrNotComparable is returned when two reports were not measured under
// comparable conditions.
var ErrNotComparable = errors.New("reports are not comparable")

// Comparable checks that two reports can be fairly compared: same hardware,
// model and load configuration, different engines, and all of those stated.
// It returns the list of problems (empty when comparable).
func Comparable(a, b *Report) []string {
	var p []string
	for _, f := range []struct{ name, x, y string }{
		{"hardware", a.Meta.Hardware, b.Meta.Hardware},
		{"model", a.Meta.Model, b.Meta.Model},
	} {
		switch {
		case f.x == "" || f.y == "":
			p = append(p, fmt.Sprintf("%s is not recorded in both reports (use --%s when running inferbench)", f.name, f.name))
		case f.x != f.y:
			p = append(p, fmt.Sprintf("%s differs: %q vs %q", f.name, f.x, f.y))
		}
	}
	if a.Meta.Engine == "" || b.Meta.Engine == "" {
		p = append(p, "engine is not recorded in both reports (use --engine)")
	} else if a.Meta.Engine == b.Meta.Engine {
		p = append(p, fmt.Sprintf("both reports use engine %q; comparing a backend with itself", a.Meta.Engine))
	}
	ca, cb := a.Config, b.Config
	if ca.Stream != cb.Stream {
		p = append(p, "stream mode differs")
	}
	if ca.MaxTokens != cb.MaxTokens {
		p = append(p, fmt.Sprintf("max_tokens differs: %d vs %d", ca.MaxTokens, cb.MaxTokens))
	}
	if filepath.Base(ca.Dataset) != filepath.Base(cb.Dataset) {
		p = append(p, fmt.Sprintf("dataset differs: %s vs %s", ca.Dataset, cb.Dataset))
	}
	if ca.Rate != cb.Rate {
		p = append(p, "request rate differs")
	}
	if !slices.Equal(ca.Concurrency, cb.Concurrency) {
		p = append(p, "concurrency levels differ")
	}
	return p
}

// PrintComparison writes a side-by-side table. Callers must have checked
// Comparable (or knowingly forced the comparison).
func PrintComparison(w io.Writer, a, b *Report) {
	fmt.Fprintf(w, "A: %s   %s\nB: %s   %s\n\n", orUnspecified(a.Meta.Engine), a.Meta.Target, orUnspecified(b.Meta.Engine), b.Meta.Target)
	fmt.Fprintf(w, "hardware  %s\nmodel     %s\n\n", orUnspecified(a.Meta.Hardware), orUnspecified(a.Meta.Model))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "conc\tmetric\tA\tB\tB/A\t")
	for i, la := range a.Levels {
		if i >= len(b.Levels) {
			break
		}
		lb := b.Levels[i]
		row := func(name string, x, y float64) {
			ratio := "-"
			if x > 0 {
				ratio = fmt.Sprintf("%.2fx", y/x)
			}
			fmt.Fprintf(tw, "%d\t%s\t%.2f\t%.2f\t%s\t\n", la.Concurrency, name, x, y, ratio)
		}
		row("req/s", la.RequestsPerSec, lb.RequestsPerSec)
		row("tok/s", la.TokensPerSec, lb.TokensPerSec)
		row("p50 ms", la.Latency.P50MS, lb.Latency.P50MS)
		row("p99 ms", la.Latency.P99MS, lb.Latency.P99MS)
		if la.TTFT != nil && lb.TTFT != nil {
			row("ttft p50 ms", la.TTFT.P50MS, lb.TTFT.P50MS)
		}
		row("failed", float64(la.Failed), float64(lb.Failed))
	}
	_ = tw.Flush()
	if note := strings.TrimSpace(a.Meta.Notes + " " + b.Meta.Notes); note != "" {
		fmt.Fprintf(w, "\nnotes: %s\n", note)
	}
}
