package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDecode(t *testing.T) {
	ttft := int64(183)
	in := Event{Type: TypeCompleted, RequestID: "r1", Backend: "vllm", Model: "m", Stream: true,
		QueueMS: 12, TTFTMS: &ttft, InputTokens: 421, OutputTokens: 180, GenerationMS: 2480, Status: StatusSuccess}
	b, _ := json.Marshal(in)
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.RequestID != "r1" || out.TTFTMS == nil || *out.TTFTMS != 183 || out.OutputTokens != 180 || out.Status != "success" {
		t.Fatalf("round trip lost data: %+v", out)
	}
	// The wire format uses snake_case names matching the documented example.
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, k := range []string{"event_type", "request_id", "queue_ms", "ttft_ms", "input_tokens", "output_tokens", "generation_ms", "status"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing wire field %q", k)
		}
	}
}

func TestDecodeRejectsBadEvents(t *testing.T) {
	for name, in := range map[string]string{
		"not json":     `{`,
		"unknown type": `{"event_type":"Nope","request_id":"x"}`,
		"no id":        `{"event_type":"InferenceStarted"}`,
		"empty":        ``,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

type recordingReporter struct {
	mu      sync.Mutex
	ok      int
	dropped int
	other   int
}

func (r *recordingReporter) EventPublished(_ string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case err == nil:
		r.ok++
	case errors.Is(err, ErrDropped):
		r.dropped++
	default:
		r.other++
	}
}

// With no reachable broker, Publish must still return immediately and shed
// events once the buffer fills: Kafka trouble can never slow a request.
func TestKafkaPublishNeverBlocks(t *testing.T) {
	rep := &recordingReporter{}
	k, err := NewKafka(KafkaConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "t", MaxBuffered: 50, Reporter: rep})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = k.Close(ctx)
	}()
	start := time.Now()
	for i := 0; i < 5000; i++ {
		k.Publish(Event{Type: TypeStarted, RequestID: "r"})
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("5000 publishes took %v with the broker down: Publish is blocking", d)
	}
	rep.mu.Lock()
	dropped := rep.dropped
	rep.mu.Unlock()
	if dropped < 4000 {
		t.Fatalf("expected most events to be dropped past the 50-record buffer, dropped=%d", dropped)
	}
}
