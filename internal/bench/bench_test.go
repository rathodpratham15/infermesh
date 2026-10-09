package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSummarizeNearestRank(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	s := Summarize(ds)
	if s.Count != 100 || s.P50MS != 50 || s.P95MS != 95 || s.P99MS != 99 || s.MinMS != 1 || s.MaxMS != 100 || s.MeanMS != 50.5 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	// Input order must not matter, and the input must not be mutated.
	rev := []time.Duration{3 * time.Millisecond, 1 * time.Millisecond, 2 * time.Millisecond}
	if got := Summarize(rev); got.P50MS != 2 || got.MaxMS != 3 {
		t.Fatalf("unsorted input: %+v", got)
	}
	if rev[0] != 3*time.Millisecond {
		t.Fatal("Summarize mutated its input")
	}
	if one := Summarize([]time.Duration{7 * time.Millisecond}); one.P99MS != 7 || one.P50MS != 7 {
		t.Fatalf("single value: %+v", one)
	}
	if empty := Summarize(nil); empty.Count != 0 {
		t.Fatalf("empty: %+v", empty)
	}
}

func TestDataset(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	d, err := LoadDataset(write("a.jsonl", "{\"prompt\":\"one\"}\n\n{\"prompt\":\"two\"}\n"))
	if err != nil || len(d.Prompts) != 2 {
		t.Fatalf("jsonl: %v %v", d, err)
	}
	if got := []string{d.Next(), d.Next(), d.Next()}; got[0] != "one" || got[1] != "two" || got[2] != "one" {
		t.Fatalf("round robin order: %v", got)
	}
	p, err := LoadDataset(write("b.txt", "first line\nsecond line\n"))
	if err != nil || p.Prompts[1] != "second line" {
		t.Fatalf("plain: %v %v", p, err)
	}
	for name, body := range map[string]string{"empty.txt": "\n\n", "bad.jsonl": "{\"nope\":1}\n", "broken.jsonl": "{oops\n"} {
		if _, err := LoadDataset(write(name, body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := LoadDataset(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: expected error")
	}
}

// chatServer simulates a gateway. delay is the per-request service time.
func chatServer(t *testing.T, delay time.Duration, peak *atomic.Int64) *httptest.Server {
	t.Helper()
	var inflight atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(delay)
		w.Header().Set("X-InferMesh-Backend", "b")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":3,"completion_tokens":7}}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func requester(url string, stream bool) *Requester {
	return &Requester{URL: url, MaxTokens: 8, Stream: stream, Timeout: 5 * time.Second, Client: NewClient(16)}
}

func TestClosedLoop(t *testing.T) {
	var peak atomic.Int64
	ts := chatServer(t, 20*time.Millisecond, &peak)
	r := RunLevel(context.Background(), requester(ts.URL, false), Single("hi"),
		LevelConfig{Concurrency: 4, Duration: 400 * time.Millisecond})

	if r.Failed != 0 || r.Succeeded != r.Requests || r.Requests < 20 {
		t.Fatalf("unexpected counts: %+v", r)
	}
	if peak.Load() != 4 {
		t.Fatalf("peak server concurrency = %d, want exactly 4", peak.Load())
	}
	if r.OutputTokens != 7*r.Succeeded || r.InputTokens != 3*r.Succeeded || r.MissingUsage != 0 {
		t.Fatalf("token accounting: %+v", r)
	}
	if r.Backends["b"] != r.Succeeded || r.TTFT != nil {
		t.Fatalf("backend/ttft: %+v", r)
	}
	if r.Latency.P50MS < 20 || r.RequestsPerSec <= 0 || r.TokensPerSec <= 0 {
		t.Fatalf("metrics: %+v", r)
	}
	// The legacy figure is successes over wall time, kept beside the steady-state one.
	want := float64(r.Succeeded) / r.WallSeconds
	if diff := r.WallRequestsPerSec - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("wall req/s %.3f != succeeded/wall %.3f", r.WallRequestsPerSec, want)
	}
	if r.ThroughputBasis != "steady" || r.DrainSeconds < 0 {
		t.Fatalf("basis=%q drain=%v", r.ThroughputBasis, r.DrainSeconds)
	}
	// 4 clients at about 20ms each can do at most ~200 req/s; the steady figure must be in that region.
	if r.RequestsPerSec < 50 || r.RequestsPerSec > 220 {
		t.Fatalf("steady req/s %.1f is implausible for 4 clients at 20ms", r.RequestsPerSec)
	}
}

func TestSteadyStateThroughputExcludesRampAndDrain(t *testing.T) {
	start := time.Now()
	at := func(sec float64) time.Time { return start.Add(time.Duration(sec * float64(time.Second))) }
	ok := func(sec float64) Result {
		return Result{CompletedAt: at(sec), Latency: time.Second, HasUsage: true, OutputTokens: 10}
	}
	// Window is 10s, so the steady interval is (2s, 10s]. Completions at 0.5s and 1.5s fall in the ramp-up
	// and at 11s and 14s in the drain: all three must be excluded from throughput.
	rs := []Result{ok(0.5), ok(1.5), ok(2.5), ok(5), ok(7.5), ok(10), ok(11), ok(14),
		{Err: "http_503", CompletedAt: at(6)}} // a failure never counts
	lr := aggregate(rs, LevelConfig{Concurrency: 4, Duration: 10 * time.Second}, start, 14*time.Second)

	if lr.ThroughputBasis != "steady" || lr.RequestsPerSec != 4.0/8.0 {
		t.Fatalf("steady req/s = %v (%s), want 4 completions / 8s = 0.5", lr.RequestsPerSec, lr.ThroughputBasis)
	}
	if lr.TokensPerSec != 40.0/8.0 {
		t.Fatalf("steady tokens/s = %v, want 5", lr.TokensPerSec)
	}
	// Legacy wall figure counts all 8 successes over 14s.
	if lr.WallRequestsPerSec != 8.0/14.0 || lr.DrainSeconds != 4 || lr.Succeeded != 8 || lr.Failed != 1 {
		t.Fatalf("wall=%v drain=%v succeeded=%d failed=%d", lr.WallRequestsPerSec, lr.DrainSeconds, lr.Succeeded, lr.Failed)
	}
}

func TestThroughputFallsBackToWallWhenNothingCompletesInWindow(t *testing.T) {
	start := time.Now()
	rs := []Result{{CompletedAt: start.Add(12 * time.Second), Latency: 12 * time.Second, HasUsage: true, OutputTokens: 5}}
	lr := aggregate(rs, LevelConfig{Concurrency: 1, Duration: 10 * time.Second}, start, 12*time.Second)
	if lr.ThroughputBasis != "wall" || lr.RequestsPerSec != lr.WallRequestsPerSec || lr.RequestsPerSec == 0 {
		t.Fatalf("expected a flagged wall-clock fallback: %+v", lr)
	}
}

// A saturated single-lane server with many waiting clients: the steady-state estimate must
// recover the server's capacity (about 20 req/s), not be dragged down by the queue at the end.
func TestSteadyThroughputUnderSaturation(t *testing.T) {
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock() // one request at a time, 50ms each => capacity 20 req/s
		time.Sleep(50 * time.Millisecond)
		mu.Unlock()
		fmt.Fprint(w, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":1,"completion_tokens":4}}`)
	}))
	defer ts.Close()
	rq := requester(ts.URL, false)
	rq.Client = NewClient(64)
	r := RunLevel(context.Background(), rq, Single("hi"), LevelConfig{Concurrency: 40, Duration: 2 * time.Second})
	if r.RequestsPerSec < 14 || r.RequestsPerSec > 24 {
		t.Fatalf("steady req/s = %.1f, want about 20 (capacity); wall-clock figure %.1f, drain %.1fs", r.RequestsPerSec, r.WallRequestsPerSec, r.DrainSeconds)
	}
	if r.DrainSeconds < 0.5 {
		t.Fatalf("expected a long drain with 40 clients queued behind one lane, got %.2fs", r.DrainSeconds)
	}
	if want := 4 * r.RequestsPerSec; r.TokensPerSec < want*0.99 || r.TokensPerSec > want*1.01 {
		t.Fatalf("tokens/s %.1f should be 4 x req/s %.1f", r.TokensPerSec, r.RequestsPerSec)
	}
}

func TestStreamUsageOption(t *testing.T) {
	var sawOpts atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_, has := raw["stream_options"]
		sawOpts.Store(has)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer ts.Close()

	rq := requester(ts.URL, true)
	rq.StreamUsage = true
	rq.Do(context.Background(), "x")
	if !sawOpts.Load() {
		t.Fatal("stream_options.include_usage must be sent for streaming requests by default")
	}
	rq.StreamUsage = false
	rq.Do(context.Background(), "x")
	if sawOpts.Load() {
		t.Fatal("stream_options must not be sent when disabled")
	}
	rq.Stream, rq.StreamUsage = false, true
	rq.Do(context.Background(), "x")
	if sawOpts.Load() {
		t.Fatal("stream_options is only meaningful when streaming and must not be sent otherwise")
	}
}

func TestWarmupIsDiscarded(t *testing.T) {
	var calls atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer ts.Close()
	r := RunLevel(context.Background(), requester(ts.URL, false), Single("hi"),
		LevelConfig{Concurrency: 1, Duration: 200 * time.Millisecond, Warmup: 200 * time.Millisecond})
	if int64(r.Requests) >= calls.Load() {
		t.Fatalf("warmup requests were counted: reported %d, server saw %d", r.Requests, calls.Load())
	}
}

func TestOpenLoopRate(t *testing.T) {
	var peak atomic.Int64
	ts := chatServer(t, 2*time.Millisecond, &peak)
	r := RunLevel(context.Background(), requester(ts.URL, false), Single("hi"),
		LevelConfig{Concurrency: 8, Duration: time.Second, Rate: 50})
	if r.Requests < 40 || r.Requests > 55 || r.DroppedArrivals != 0 {
		t.Fatalf("50 req/s for 1s sent %d requests (dropped %d)", r.Requests, r.DroppedArrivals)
	}
}

func TestOpenLoopDropsArrivalsAtCap(t *testing.T) {
	var peak atomic.Int64
	ts := chatServer(t, 200*time.Millisecond, &peak)
	r := RunLevel(context.Background(), requester(ts.URL, false), Single("hi"),
		LevelConfig{Concurrency: 2, Duration: 500 * time.Millisecond, Rate: 100})
	if r.DroppedArrivals == 0 {
		t.Fatalf("expected dropped arrivals when offered load exceeds capacity: %+v", r)
	}
	if peak.Load() > 2 {
		t.Fatalf("in-flight exceeded the cap: %d", peak.Load())
	}
}

const sseRole = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"

func TestStreamingTTFTSkipsRoleChunk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl := w.(http.Flusher)
		fmt.Fprint(w, sseRole) // role chunk arrives immediately but is not a token
		fl.Flush()
		time.Sleep(80 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fl.Flush()
		time.Sleep(40 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	res := requester(ts.URL, true).Do(context.Background(), "hi")
	if res.Err != "" || !res.HasTTFT || !res.HasUsage || res.OutputTokens != 5 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.TTFT < 70*time.Millisecond {
		t.Fatalf("TTFT %v counted the role chunk instead of the first content token", res.TTFT)
	}
	if res.Latency < res.TTFT+30*time.Millisecond {
		t.Fatalf("latency %v should exceed TTFT %v by the decode time", res.Latency, res.TTFT)
	}
}

func TestFailuresAreCountedByClass(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		stream  bool
		timeout time.Duration
	}{
		"http_503": {func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusServiceUnavailable) }, false, 0},
		"http_429": {func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusTooManyRequests) }, true, 0},
		"stream_error": {func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, sseRole+"data: {\"error\":{\"message\":\"boom\"}}\n\n")
		}, true, 0},
		"stream_truncated": {func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, sseRole+"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		}, true, 0},
		"timeout": {func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client giving up
			<-r.Context().Done()
		}, false, 50 * time.Millisecond},
	}
	for want, c := range cases {
		ts := httptest.NewServer(c.handler)
		rq := requester(ts.URL, c.stream)
		if c.timeout > 0 {
			rq.Timeout = c.timeout
		}
		res := rq.Do(context.Background(), "hi")
		ts.Close()
		if res.Err != want {
			t.Errorf("want error class %q, got %q", want, res.Err)
		}
	}
	// A refused connection is a failure too, not a panic or a silent drop.
	if res := requester("http://127.0.0.1:1/v1/chat/completions", false).Do(context.Background(), "x"); res.Err != "connection" {
		t.Errorf("connection refused classified as %q", res.Err)
	}
}

func TestFailedRequestsExcludedFromLatency(t *testing.T) {
	var n atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1)%2 == 0 {
			http.Error(w, "x", 500)
			return
		}
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"x"}}]}`) // no usage
	}))
	defer ts.Close()
	r := RunLevel(context.Background(), requester(ts.URL, false), Single("x"),
		LevelConfig{Concurrency: 1, Duration: 300 * time.Millisecond})
	if r.Failed == 0 || r.Succeeded == 0 || r.Errors["http_500"] != r.Failed {
		t.Fatalf("counts: %+v", r)
	}
	if r.Latency.Count != r.Succeeded || r.Latency.MinMS < 25 {
		t.Fatalf("fast failures leaked into the latency distribution: %+v", r.Latency)
	}
	if r.MissingUsage != r.Succeeded || r.OutputTokens != 0 {
		t.Fatalf("missing usage must be reported, not guessed: %+v", r)
	}
}

func TestReportRoundTripAndTable(t *testing.T) {
	rep := &Report{Tool: "inferbench", Meta: Meta{Hardware: "M3", Model: "m", Engine: "e", Target: "http://x"},
		Config: Config{Concurrency: []int{1}, Stream: true, Dataset: "d.jsonl", Duration: "1s", MaxTokens: 8},
		Levels: []LevelResult{{Concurrency: 1, Requests: 3, Succeeded: 2, Failed: 1, Errors: map[string]int{"http_503": 1},
			TTFT: &Summary{P50MS: 5}, Latency: Summary{P50MS: 10}}}}
	path := filepath.Join(t.TempDir(), "r.json")
	if err := rep.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReport(path)
	if err != nil || got.Meta.Hardware != "M3" || got.Levels[0].Errors["http_503"] != 1 || got.Levels[0].TTFT.P50MS != 5 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	var sb strings.Builder
	rep.PrintHeader(&sb)
	rep.PrintTable(&sb)
	for _, want := range []string{"hardware  M3", "ttft p50", "errors: http_503=1"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("output missing %q:\n%s", want, sb.String())
		}
	}
	// Unspecified context must be visible, not blank.
	var sb2 strings.Builder
	(&Report{}).PrintHeader(&sb2)
	if !strings.Contains(sb2.String(), "(unspecified)") {
		t.Errorf("missing '(unspecified)' marker:\n%s", sb2.String())
	}
}

func TestComparable(t *testing.T) {
	mk := func(engine string) *Report {
		return &Report{Meta: Meta{Hardware: "A100", Model: "m", Engine: engine},
			Config: Config{Concurrency: []int{1, 5}, Stream: true, MaxTokens: 128, Dataset: "benchmarks/datasets/short.jsonl"}}
	}
	if p := Comparable(mk("vllm"), mk("triton")); len(p) != 0 {
		t.Fatalf("should be comparable, got %v", p)
	}
	b := mk("triton")
	b.Meta.Hardware = "L4"
	b.Config.MaxTokens = 64
	b.Config.Dataset = "other/long.jsonl"
	if p := Comparable(mk("vllm"), b); len(p) != 3 {
		t.Fatalf("want 3 problems, got %v", p)
	}
	if p := Comparable(mk("vllm"), mk("vllm")); len(p) != 1 || !strings.Contains(p[0], "itself") {
		t.Fatalf("same engine: %v", p)
	}
	unlabeled := mk("triton")
	unlabeled.Meta.Hardware, unlabeled.Meta.Model = "", ""
	if p := Comparable(mk("vllm"), unlabeled); len(p) != 2 {
		t.Fatalf("unlabeled reports must not be comparable: %v", p)
	}
}
