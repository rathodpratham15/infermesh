package bench

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// LevelConfig describes one load level.
type LevelConfig struct {
	Concurrency int           // workers (closed loop) or max in-flight (open loop)
	Duration    time.Duration // time to keep starting new requests
	Rate        float64       // requests/second; 0 = closed loop (each worker sends back to back)
	Warmup      time.Duration // sent first and discarded
}

// LevelResult is the measured outcome of one load level.
type LevelResult struct {
	Concurrency     int            `json:"concurrency"`
	TargetRate      float64        `json:"target_rate_rps,omitempty"`
	WallSeconds     float64        `json:"wall_seconds"`
	Requests        int            `json:"requests"`
	Succeeded       int            `json:"succeeded"`
	Failed          int            `json:"failed"`
	RequestsPerSec  float64        `json:"requests_per_sec"`
	InputTokens     int            `json:"input_tokens"`
	OutputTokens    int            `json:"output_tokens"`
	TokensPerSec    float64        `json:"output_tokens_per_sec"`
	Latency         Summary        `json:"latency"`
	TTFT            *Summary       `json:"ttft,omitempty"`
	Errors          map[string]int `json:"errors,omitempty"`
	Backends        map[string]int `json:"backends,omitempty"`
	DroppedArrivals int            `json:"dropped_arrivals,omitempty"`
	MissingUsage    int            `json:"missing_usage,omitempty"`
}

// NewClient returns an HTTP client sized for the given maximum concurrency.
func NewClient(maxConcurrency int) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = maxConcurrency * 2
	t.MaxIdleConnsPerHost = maxConcurrency * 2
	t.MaxConnsPerHost = 0
	return &http.Client{Transport: t}
}

// RunLevel drives one load level and returns its measurements. Requests that
// start before Duration elapses are allowed to finish, and are counted.
func RunLevel(ctx context.Context, req *Requester, ds *Dataset, cfg LevelConfig) LevelResult {
	if cfg.Warmup > 0 {
		wctx, cancel := context.WithTimeout(ctx, cfg.Warmup)
		drive(wctx, req, ds, cfg, nil)
		cancel()
	}
	var mu sync.Mutex
	var results []Result
	collect := func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}

	rctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()
	start := time.Now()
	dropped := drive(rctx, req, ds, cfg, collect)
	wall := time.Since(start)

	lr := aggregate(results, cfg, wall)
	lr.DroppedArrivals = dropped
	return lr
}

// drive sends requests until ctx ends and waits for in-flight ones. It returns
// the number of arrivals dropped because the concurrency cap was reached
// (open loop only). report may be nil (warmup).
//
// Requests use context.WithoutCancel so one that is in flight when the load
// window ends completes and is measured, instead of being cut off.
func drive(ctx context.Context, req *Requester, ds *Dataset, cfg LevelConfig, report func(Result)) (dropped int) {
	send := func() {
		r := req.Do(context.WithoutCancel(ctx), ds.Next())
		if report != nil {
			report(r)
		}
	}
	var wg sync.WaitGroup

	if cfg.Rate <= 0 { // closed loop
		for i := 0; i < cfg.Concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					send()
				}
			}()
		}
		wg.Wait()
		return 0
	}

	// Open loop: arrivals follow the clock, independent of response times, so a
	// slow server shows up as queueing/drops rather than as a reduced offered load.
	sem := make(chan struct{}, cfg.Concurrency)
	interval := time.Duration(float64(time.Second) / cfg.Rate)
	t0 := time.Now()
	var drops atomic.Int64
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			wg.Wait()
			return int(drops.Load())
		case <-time.After(time.Until(t0.Add(time.Duration(n) * interval))):
		}
		select {
		case sem <- struct{}{}:
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				send()
			}()
		default:
			drops.Add(1)
		}
	}
}

func aggregate(rs []Result, cfg LevelConfig, wall time.Duration) LevelResult {
	lr := LevelResult{Concurrency: cfg.Concurrency, TargetRate: cfg.Rate, WallSeconds: wall.Seconds(), Requests: len(rs)}
	var lat, ttft []time.Duration
	for _, r := range rs {
		if r.Err != "" {
			lr.Failed++
			if lr.Errors == nil {
				lr.Errors = map[string]int{}
			}
			lr.Errors[r.Err]++
			continue
		}
		lr.Succeeded++
		lat = append(lat, r.Latency)
		if r.HasTTFT {
			ttft = append(ttft, r.TTFT)
		}
		if r.HasUsage {
			lr.InputTokens += r.InputTokens
			lr.OutputTokens += r.OutputTokens
		} else {
			lr.MissingUsage++
		}
		if r.Backend != "" {
			if lr.Backends == nil {
				lr.Backends = map[string]int{}
			}
			lr.Backends[r.Backend]++
		}
	}
	lr.Latency = Summarize(lat)
	if len(ttft) > 0 {
		s := Summarize(ttft)
		lr.TTFT = &s
	}
	if wall > 0 {
		lr.RequestsPerSec = float64(lr.Succeeded) / wall.Seconds()
		lr.TokensPerSec = float64(lr.OutputTokens) / wall.Seconds()
	}
	return lr
}

// SortedKeys returns the keys of m in order, for stable output.
func SortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
