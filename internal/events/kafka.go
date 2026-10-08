package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Reporter is told the outcome of each publish attempt, for metrics. A nil
// error means the broker acknowledged the record.
type Reporter interface {
	EventPublished(eventType string, err error)
}

// KafkaConfig configures the producer.
type KafkaConfig struct {
	Brokers  []string
	Topic    string
	ClientID string
	// MaxBuffered bounds records held in memory while the broker is slow or
	// down. Beyond it new events are dropped rather than blocking requests.
	MaxBuffered int
	Reporter    Reporter // optional
}

// Kafka publishes events to a topic. Publish uses TryProduce, which never
// blocks: if the client's buffer is full the event is dropped and reported.
type Kafka struct {
	cl       *kgo.Client
	topic    string
	reporter Reporter
}

func NewKafka(cfg KafkaConfig) (*Kafka, error) {
	if cfg.MaxBuffered <= 0 {
		cfg.MaxBuffered = 10000
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(or(cfg.ClientID, "infermesh-gateway")),
		kgo.MaxBufferedRecords(cfg.MaxBuffered),
		kgo.ProducerLinger(20 * time.Millisecond), // small batches, low added delay
		kgo.RecordDeliveryTimeout(30 * time.Second),
		kgo.RequiredAcks(kgo.LeaderAck()),
		kgo.DisableIdempotentWrite(), // telemetry is at-least-once and best effort
		kgo.AllowAutoTopicCreation(),
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Kafka{cl: cl, topic: cfg.Topic, reporter: cfg.Reporter}, nil
}

func (k *Kafka) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		k.report(e.Type, err)
		return
	}
	rec := &kgo.Record{Topic: k.topic, Key: []byte(e.RequestID), Value: b}
	// A background context: delivery must outlive the request that produced it.
	k.cl.TryProduce(context.Background(), rec, func(_ *kgo.Record, err error) {
		k.report(e.Type, err)
	})
}

func (k *Kafka) report(eventType string, err error) {
	if k.reporter != nil {
		k.reporter.EventPublished(eventType, err)
	}
}

// ErrDropped marks events dropped because the producer buffer was full.
var ErrDropped = kgo.ErrMaxBuffered

// Close flushes buffered events (bounded by ctx) and closes the client.
func (k *Kafka) Close(ctx context.Context) error {
	err := k.cl.Flush(ctx)
	k.cl.Close()
	return err
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
