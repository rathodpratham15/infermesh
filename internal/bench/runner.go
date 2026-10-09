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
	Concurrency  int     `json:"concurrency"`
	TargetRate   float64 `json:"target_rate_rps,omitempty"`
	WallSeconds  float64 `json:"wall_seconds"`
	DrainSeconds float64 `json:"drain_seconds"`
	Requests     int     `json:"requests"`
	Succeeded    int     `json:"succeeded"`
	Failed       int     `json:"failed"`
	// RequestsPerSec and TokensPerSec are STEADY-STATE throughput: completions inside the load
	// window after the ramp-up (the last 80% of it), divided by that interval. They exclude both
	// the ramp-up and the drain of requests still in flight when the window closes.
	RequestsPerSec float64 `json:"requests_per_sec"`
	TokensPerSec   float64 `json:"output_tokens_per_sec"`
	// ThroughputBasis is "steady" normally, or "wall" if no request completed inside the steady
	// window (requests slower than the window), in which case the wall-clock figures are used.
	ThroughputBasis string `json:"throughput_basis"`
	// WallRequestsPerSec and WallTokensPerSec divide ALL successes by total wall time (including
	// ramp-up and drain). Kept so the two definitions can be compared.
	WallRequestsPerSec float64        `json:"wall_requests_per_sec"`
	WallTokensPerSec   float64        `json:"wall_output_tokens_per_sec"`
	InputTokens        int            `json:"input_tokens"`
	OutputTokens       int            `json:"output_tokens"`
	Latency            Summary        `json:"latency"`
	TTFT               *Summary       `json:"ttft,omitempty"`
	Errors             map[string]int `json:"errors,omitempty"`
	Backends           map[string]int `json:"backends,omitempty"`
	DroppedArrivals    int            `json:"dropped_arrivals,omitempty"`
	MissingUsage       int            `json:"missing_usage,omitempty"`
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

	lr := aggregate(results, cfg, start, wall)
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

// steadyStartFraction is the share of the load window treated as ramp-up. In a closed loop
// every client always has a request in flight, so counting completions from t=0 undercounts
// by the carry-out at the end of the window (about one request per client); after the first
// round of requests the in-flight count is the same at both ends of the interval, so
// completions in the interval equal the work done in it.
const steadyStartFraction = 0.2

func aggregate(rs []Result, cfg LevelConfig, start time.Time, wall time.Duration) LevelResult {
	lr := LevelResult{Concurrency: cfg.Concurrency, TargetRate: cfg.Rate, WallSeconds: wall.Seconds(), Requests: len(rs)}
	if d := wall - cfg.Duration; d > 0 {
		lr.DrainSeconds = d.Seconds()
	}
	steadyFrom := time.Duration(steadyStartFraction * float64(cfg.Duration))
	var inSteady, steadyTokens int
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
		if off := r.CompletedAt.Sub(start); off > steadyFrom && off <= cfg.Duration {
			inSteady++
			if r.HasUsage {
				steadyTokens += r.OutputTokens
			}
		}
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
		lr.WallRequestsPerSec = float64(lr.Succeeded) / wall.Seconds()
		lr.WallTokensPerSec = float64(lr.OutputTokens) / wall.Seconds()
	}
	if window := (cfg.Duration - steadyFrom).Seconds(); inSteady > 0 && window > 0 {
		lr.ThroughputBasis = "steady"
		lr.RequestsPerSec = float64(inSteady) / window
		lr.TokensPerSec = float64(steadyTokens) / window
	} else {
		lr.ThroughputBasis = "wall"
		lr.RequestsPerSec, lr.TokensPerSec = lr.WallRequestsPerSec, lr.WallTokensPerSec
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
