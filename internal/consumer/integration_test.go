package consumer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/rathodpratham15/infermesh/internal/config"
	"github.com/rathodpratham15/infermesh/internal/events"
)

// TestKafkaRoundTrip publishes through the real producer, consumes through the
// real consumer group, and checks the resulting metrics. It needs a broker and
// is skipped unless INFERMESH_TEST_KAFKA_BROKERS is set.
func TestKafkaRoundTrip(t *testing.T) {
	brokers := config.SplitList(os.Getenv("INFERMESH_TEST_KAFKA_BROKERS"))
	if len(brokers) == 0 {
		t.Skip("INFERMESH_TEST_KAFKA_BROKERS not set")
	}
	topic := fmt.Sprintf("infermesh.test.%d", time.Now().UnixNano())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	pub, err := events.NewKafka(events.KafkaConfig{Brokers: brokers, Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	ttft := int64(150)
	const n = 20
	for i := 0; i < n; i++ {
		pub.Publish(events.Event{Type: events.TypeCompleted, RequestID: fmt.Sprintf("r%d", i), Backend: "vllm",
			Status: events.StatusSuccess, InputTokens: 10, OutputTokens: 5, TTFTMS: &ttft, GenerationMS: 500, Attempt: 1})
	}
	// One poison record must be skipped without stalling the partition.
	poison, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		t.Fatal(err)
	}
	defer poison.Close()
	if err := poison.ProduceSync(context.Background(), &kgo.Record{Topic: topic, Value: []byte("not json")}).FirstErr(); err != nil {
		t.Fatal(err)
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pub.Close(flushCtx); err != nil {
		t.Fatal(err)
	}

	agg, scrape := newAgg(t)
	c, err := New(Config{Brokers: brokers, Topic: topic, Group: topic + "-g"}, agg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.Now().Add(30 * time.Second)
	for {
		m := scrape()
		if strings.Contains(m, fmt.Sprintf(`infermesh_telemetry_events_total{backend="vllm",event_type="InferenceCompleted",status="success"} %d`, n)) &&
			strings.Contains(m, `infermesh_telemetry_events_invalid_total{reason="decode"} 1`) {
			want(t, m,
				fmt.Sprintf(`infermesh_telemetry_tokens_output_total{backend="vllm"} %d`, n*5),
				fmt.Sprintf(`infermesh_telemetry_ttft_seconds_count{backend="vllm"} %d`, n))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for events to be aggregated:\n%s", m)
		}
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
