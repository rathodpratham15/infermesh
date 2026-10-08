package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Result is the outcome of one request.
type Result struct {
	Latency      time.Duration
	TTFT         time.Duration // streaming only; 0 when no content token arrived
	HasTTFT      bool
	OutputTokens int
	InputTokens  int
	HasUsage     bool
	Backend      string
	Err          string // empty on success; a short class such as "http_503"
}

// Requester sends one chat completion and measures it.
type Requester struct {
	URL       string // full URL of /v1/chat/completions
	Model     string
	MaxTokens int
	Stream    bool
	Timeout   time.Duration
	Client    *http.Client
}

type chatBody struct {
	Model     string        `json:"model,omitempty"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respBody struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Do sends prompt and returns the measurement. It never returns an error:
// failures are recorded in Result.Err so they are counted, not hidden.
func (r *Requester) Do(ctx context.Context, prompt string) Result {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	body, _ := json.Marshal(chatBody{
		Model: r.Model, MaxTokens: r.MaxTokens, Stream: r.Stream,
		Messages: []chatMessage{{Role: "user", Content: prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(body))
	if err != nil {
		return Result{Err: "build_request"}
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := r.Client.Do(req)
	if err != nil {
		return Result{Latency: time.Since(start), Err: classify(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	res := Result{Backend: resp.Header.Get("X-InferMesh-Backend")}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		res.Latency = time.Since(start)
		res.Err = fmt.Sprintf("http_%d", resp.StatusCode)
		return res
	}

	if !r.Stream {
		var rb respBody
		if err := json.NewDecoder(resp.Body).Decode(&rb); err != nil {
			res.Latency, res.Err = time.Since(start), classify(err)
			return res
		}
		res.Latency = time.Since(start)
		res.setUsage(rb)
		return res
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	done := false
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			done = true
			break
		}
		var rb respBody
		if err := json.Unmarshal([]byte(data), &rb); err != nil {
			res.Latency, res.Err = time.Since(start), "bad_chunk"
			return res
		}
		if rb.Error != nil {
			res.Latency, res.Err = time.Since(start), "stream_error"
			return res
		}
		if !res.HasTTFT && len(rb.Choices) > 0 && rb.Choices[0].Delta.Content != "" {
			res.TTFT, res.HasTTFT = time.Since(start), true
		}
		res.setUsage(rb)
	}
	res.Latency = time.Since(start)
	switch {
	case sc.Err() != nil:
		res.Err = classify(sc.Err())
	case !done:
		res.Err = "stream_truncated"
	}
	return res
}

func (r *Result) setUsage(rb respBody) {
	if rb.Usage != nil {
		r.InputTokens, r.OutputTokens, r.HasUsage = rb.Usage.PromptTokens, rb.Usage.CompletionTokens, true
	}
}

func classify(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "connection"
}
