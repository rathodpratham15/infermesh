package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// Host describes the machine running inferbench (not necessarily the server).
type Host struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	NumCPU    int    `json:"num_cpu"`
	GoVersion string `json:"go_version"`
}

// Meta records what was measured. Hardware, Model and Engine are supplied by the
// operator because the benchmark client cannot know what the server runs on; they
// travel with every result so a number is never separated from its context.
type Meta struct {
	Hardware      string          `json:"hardware"`
	Model         string          `json:"model"`
	Engine        string          `json:"engine"`
	Notes         string          `json:"notes,omitempty"`
	Scenario      string          `json:"scenario,omitempty"`
	Target        string          `json:"target"`
	Host          Host            `json:"client_host"`
	GatewayBefore json.RawMessage `json:"gateway_backends_before,omitempty"`
	GatewayAfter  json.RawMessage `json:"gateway_backends_after,omitempty"`
}

// Config is the load configuration, recorded for reproducibility.
type Config struct {
	Concurrency    []int   `json:"concurrency"`
	Duration       string  `json:"duration"`
	Warmup         string  `json:"warmup,omitempty"`
	Rate           float64 `json:"rate_rps,omitempty"`
	Stream         bool    `json:"stream"`
	StreamUsage    bool    `json:"stream_usage"`
	MaxTokens      int     `json:"max_tokens"`
	RequestedModel string  `json:"requested_model,omitempty"`
	Dataset        string  `json:"dataset"`
	DatasetPrompts int     `json:"dataset_prompts"`
	Timeout        string  `json:"timeout"`
}

// Report is the JSON results file.
type Report struct {
	Tool       string        `json:"tool"`
	Version    string        `json:"version"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Meta       Meta          `json:"meta"`
	Config     Config        `json:"config"`
	Levels     []LevelResult `json:"levels"`
}

func (r *Report) WriteJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func ReadReport(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

func orUnspecified(s string) string {
	if s == "" {
		return "(unspecified)"
	}
	return s
}

// PrintHeader writes the context block shown above the results.
func (r *Report) PrintHeader(w io.Writer) {
	fmt.Fprintf(w, "target    %s\n", r.Meta.Target)
	fmt.Fprintf(w, "engine    %s\n", orUnspecified(r.Meta.Engine))
	fmt.Fprintf(w, "model     %s\n", orUnspecified(r.Meta.Model))
	fmt.Fprintf(w, "hardware  %s\n", orUnspecified(r.Meta.Hardware))
	mode := "closed loop"
	if r.Config.Rate > 0 {
		mode = fmt.Sprintf("open loop @ %.1f req/s", r.Config.Rate)
	}
	fmt.Fprintf(w, "load      %s, %s, stream=%v, max_tokens=%d, duration=%s per level\n",
		mode, r.Config.Dataset, r.Config.Stream, r.Config.MaxTokens, r.Config.Duration)
	if r.Meta.Scenario != "" {
		fmt.Fprintf(w, "scenario  %s\n", r.Meta.Scenario)
	}
	fmt.Fprintln(w)
}

// PrintTable writes the human-readable results.
func (r *Report) PrintTable(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	stream := r.Config.Stream
	head := "conc\treqs\tok\tfail\treq/s\ttok/s\tp50 ms\tp95 ms\tp99 ms\t"
	if stream {
		head += "ttft p50\tttft p95\tttft p99\t"
	}
	fmt.Fprintln(tw, head)
	for _, l := range r.Levels {
		row := fmt.Sprintf("%d\t%d\t%d\t%d\t%.2f\t%.1f\t%.0f\t%.0f\t%.0f\t",
			l.Concurrency, l.Requests, l.Succeeded, l.Failed, l.RequestsPerSec, l.TokensPerSec,
			l.Latency.P50MS, l.Latency.P95MS, l.Latency.P99MS)
		if stream {
			if l.TTFT != nil {
				row += fmt.Sprintf("%.0f\t%.0f\t%.0f\t", l.TTFT.P50MS, l.TTFT.P95MS, l.TTFT.P99MS)
			} else {
				row += "-\t-\t-\t"
			}
		}
		fmt.Fprintln(tw, row)
	}
	_ = tw.Flush()

	for _, l := range r.Levels {
		var notes []string
		if l.Failed > 0 {
			var parts []string
			for _, k := range SortedKeys(l.Errors) {
				parts = append(parts, fmt.Sprintf("%s=%d", k, l.Errors[k]))
			}
			notes = append(notes, "errors: "+strings.Join(parts, " "))
		}
		if len(l.Backends) > 1 {
			var parts []string
			for _, k := range SortedKeys(l.Backends) {
				parts = append(parts, fmt.Sprintf("%s=%d", k, l.Backends[k]))
			}
			notes = append(notes, "backends: "+strings.Join(parts, " "))
		}
		if l.DroppedArrivals > 0 {
			notes = append(notes, fmt.Sprintf("%d arrivals dropped at the concurrency cap (server could not keep up with the offered rate)", l.DroppedArrivals))
		}
		if l.ThroughputBasis == "wall" {
			notes = append(notes, "no request completed inside the steady-state window (requests are slower than it); throughput falls back to wall-clock and includes ramp-up and drain")
		}
		if l.MissingUsage > 0 {
			notes = append(notes, fmt.Sprintf("%d responses had no token usage, so tokens/s undercounts", l.MissingUsage))
		}
		for _, n := range notes {
			fmt.Fprintf(w, "  c=%d  %s\n", l.Concurrency, n)
		}
	}
}
