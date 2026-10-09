// Command inferbench load-tests an InferMesh gateway (or any OpenAI-compatible
// chat endpoint) and reports latency, TTFT and throughput.
//
//	inferbench --target http://localhost:8080 --concurrency 1,5,10,25,50 --duration 60s
//	inferbench --scenario benchmarks/scenarios/b-increasing-concurrency.json
//	inferbench compare vllm.json triton.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rathodpratham15/infermesh/internal/bench"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const defaultPrompt = "Explain in two sentences why batching improves GPU utilization during LLM inference."

type scenario struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Concurrency []int    `json:"concurrency"`
	Duration    string   `json:"duration"`
	Warmup      string   `json:"warmup"`
	Rate        *float64 `json:"rate"`
	Dataset     string   `json:"dataset"`
	Stream      *bool    `json:"stream"`
	MaxTokens   *int     `json:"max_tokens"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "compare" {
		os.Exit(compareCmd(os.Args[2:]))
	}
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "inferbench:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("inferbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		target      = fs.String("target", "http://localhost:8080", "base URL of the gateway")
		concurrency = fs.String("concurrency", "1", "comma-separated concurrency levels, e.g. 1,5,10,25,50")
		duration    = fs.Duration("duration", 60*time.Second, "how long each level keeps starting requests")
		warmup      = fs.Duration("warmup", 0, "warmup sent (and discarded) before each level")
		rate        = fs.Float64("rate", 0, "requests/second per level (open loop); 0 = closed loop")
		dataset     = fs.String("dataset", "", "prompts: JSONL ({\"prompt\":...}) or one per line; default is one built-in prompt")
		stream      = fs.Bool("stream", false, "use streaming responses (enables TTFT)")
		streamUsage = fs.Bool("stream-usage", true, "ask for token usage in streamed responses (stream_options.include_usage); disable for servers that reject it")
		maxTokens   = fs.Int("max-tokens", 128, "max_tokens per request")
		model       = fs.String("model", "", "model name sent in requests, also recorded in the report")
		timeout     = fs.Duration("timeout", 2*time.Minute, "per-request timeout")
		output      = fs.String("output", "", "JSON results path (default benchmarks/results/<name>-<timestamp>.json)")
		hardware    = fs.String("hardware", "", "hardware the SERVER runs on, e.g. \"Apple M3 16GB\" (recorded in the report)")
		engine      = fs.String("engine", "", "serving engine under test, e.g. vllm or triton-trtllm (recorded in the report)")
		notes       = fs.String("notes", "", "free-form notes recorded in the report")
		scenarioArg = fs.String("scenario", "", "JSON scenario file; explicit flags override its values")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	var sc scenario
	if *scenarioArg != "" {
		b, err := os.ReadFile(*scenarioArg)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &sc); err != nil {
			return fmt.Errorf("%s: %w", *scenarioArg, err)
		}
		applyScenario(sc, explicit, concurrency, duration, warmup, rate, dataset, stream, maxTokens)
	}

	levels, err := parseLevels(*concurrency)
	if err != nil {
		return err
	}
	if *duration <= 0 || *rate < 0 || *maxTokens < 1 {
		return errors.New("duration must be positive, rate non-negative and max-tokens at least 1")
	}

	ds := bench.Single(defaultPrompt)
	if *dataset != "" {
		if ds, err = bench.LoadDataset(*dataset); err != nil {
			return err
		}
	}

	base := strings.TrimRight(*target, "/")
	note, err := checkHealth(base)
	if err != nil {
		return err
	}
	if note != "" {
		fmt.Fprintln(stderr, "note:", note)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	maxC := 0
	for _, l := range levels {
		maxC = max(maxC, l)
	}
	req := &bench.Requester{
		URL: base + "/v1/chat/completions", Model: *model, MaxTokens: *maxTokens,
		Stream: *stream, StreamUsage: *streamUsage, Timeout: *timeout, Client: bench.NewClient(maxC),
	}

	rep := &bench.Report{
		Tool: "inferbench", Version: version, StartedAt: time.Now().UTC(),
		Meta: bench.Meta{
			Hardware: *hardware, Model: *model, Engine: *engine, Notes: *notes, Scenario: sc.Name, Target: base,
			Host:          bench.Host{OS: runtime.GOOS, Arch: runtime.GOARCH, NumCPU: runtime.NumCPU(), GoVersion: runtime.Version()},
			GatewayBefore: fetchBackends(base),
		},
		Config: bench.Config{
			Concurrency: levels, Duration: duration.String(), Rate: *rate, Stream: *stream, StreamUsage: *streamUsage && *stream, MaxTokens: *maxTokens,
			RequestedModel: *model, Dataset: ds.Name, DatasetPrompts: len(ds.Prompts), Timeout: timeout.String(),
		},
	}
	if *warmup > 0 {
		rep.Config.Warmup = warmup.String()
	}
	rep.PrintHeader(stdout)

	interrupted := false
	for _, c := range levels {
		if ctx.Err() != nil {
			interrupted = true
			break
		}
		fmt.Fprintf(stderr, "running concurrency=%d for %s ...\n", c, *duration)
		lr := bench.RunLevel(ctx, req, ds, bench.LevelConfig{Concurrency: c, Duration: *duration, Rate: *rate, Warmup: *warmup})
		if ctx.Err() != nil {
			interrupted = true // the level was cut short, so its numbers are not comparable
			break
		}
		rep.Levels = append(rep.Levels, lr)
	}
	rep.FinishedAt = time.Now().UTC()
	rep.Meta.GatewayAfter = fetchBackends(base)

	rep.PrintTable(stdout)
	if interrupted {
		fmt.Fprintln(stdout, "\ninterrupted: the level in progress was discarded; only completed levels are reported")
	}
	if len(rep.Levels) == 0 {
		return errors.New("no level completed")
	}

	out := *output
	if out == "" {
		name := sc.Name
		if name == "" {
			name = "run"
		}
		out = filepath.Join("benchmarks", "results", fmt.Sprintf("%s-%s.json", name, rep.StartedAt.Format("20060102-150405")))
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := rep.WriteJSON(out); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nresults written to %s\n", out)
	return nil
}

// applyScenario fills in flags the user did not set explicitly.
func applyScenario(sc scenario, explicit map[string]bool, conc *string, dur, warm *time.Duration, rate *float64, ds *string, stream *bool, maxTok *int) {
	if len(sc.Concurrency) > 0 && !explicit["concurrency"] {
		parts := make([]string, len(sc.Concurrency))
		for i, c := range sc.Concurrency {
			parts[i] = strconv.Itoa(c)
		}
		*conc = strings.Join(parts, ",")
	}
	if d, err := time.ParseDuration(sc.Duration); err == nil && !explicit["duration"] {
		*dur = d
	}
	if d, err := time.ParseDuration(sc.Warmup); err == nil && !explicit["warmup"] {
		*warm = d
	}
	if sc.Rate != nil && !explicit["rate"] {
		*rate = *sc.Rate
	}
	if sc.Dataset != "" && !explicit["dataset"] {
		*ds = sc.Dataset
	}
	if sc.Stream != nil && !explicit["stream"] {
		*stream = *sc.Stream
	}
	if sc.MaxTokens != nil && !explicit["max-tokens"] {
		*maxTok = *sc.MaxTokens
	}
}

func parseLevels(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid concurrency level %q", p)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one concurrency level is required")
	}
	return out, nil
}

// checkHealth confirms the target is reachable. A target without /healthz (a bare
// engine such as vLLM or mlx_lm.server, benchmarked directly to measure gateway
// overhead) answers 404 there; that is accepted with a note, any other failure is not.
func checkHealth(base string) (note string, err error) {
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(base + "/healthz")
	if err != nil {
		return "", fmt.Errorf("target not reachable: %w", err)
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return "", nil
	case http.StatusNotFound:
		return "target has no /healthz; assuming a bare engine and continuing", nil
	}
	return "", fmt.Errorf("target /healthz returned %d", resp.StatusCode)
}

// fetchBackends snapshots the gateway's /v1/backends (best effort) so the report
// records which backends were in play and their counters.
func fetchBackends(base string) json.RawMessage {
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(base + "/v1/backends")
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || !json.Valid(b) {
		return nil
	}
	return b
}

func compareCmd(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	force := fs.Bool("force", false, "print the comparison even if the reports are not comparable")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: inferbench compare [--force] <a.json> <b.json>")
		return 2
	}
	a, err := bench.ReadReport(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "inferbench:", err)
		return 1
	}
	b, err := bench.ReadReport(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "inferbench:", err)
		return 1
	}
	if problems := bench.Comparable(a, b); len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "these reports are not comparable:")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  -", p)
		}
		if !*force {
			fmt.Fprintln(os.Stderr, "refusing to compare (use --force to print anyway)")
			return 1
		}
		fmt.Fprintln(os.Stderr, "--force given: the table below is NOT a fair comparison")
	}
	bench.PrintComparison(os.Stdout, a, b)
	return 0
}
