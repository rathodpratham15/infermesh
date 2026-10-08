package consumer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/rathodpratham15/infermesh/internal/events"
)

// Config selects what to consume.
type Config struct {
	Brokers  []string
	Topic    string
	Group    string
	ClientID string
}

// Consumer is a Kafka consumer-group member feeding an Aggregator.
type Consumer struct {
	cl  *kgo.Client
	agg *Aggregator
	log *slog.Logger
}

// New creates a consumer. Offsets are committed only after a record has been
// aggregated (at-least-once): after a crash a few events may be counted twice,
// but none are lost. A new group starts from the earliest available offset.
func New(cfg Config, agg *Aggregator, log *slog.Logger) (*Consumer, error) {
	if cfg.ClientID == "" {
		cfg.ClientID = "infermesh-telemetry-consumer"
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.AutoCommitMarks(),
		kgo.BlockRebalanceOnPoll(),
	)
	if err != nil {
		return nil, err
	}
	return &Consumer{cl: cl, agg: agg, log: log}, nil
}

// Run consumes until ctx is cancelled, then commits marked offsets and leaves
// the group.
func (c *Consumer) Run(ctx context.Context) error {
	defer c.cl.Close()
	for {
		fetches := c.cl.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			// Required with BlockRebalanceOnPoll: without it, Close() waits
			// forever for the poll to release its rebalance block.
			c.cl.AllowRebalance()
			c.commit()
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			// Broker unreachable, unknown topic yet, rebalance: all transient.
			c.log.Warn("fetch error", "topic", topic, "partition", partition, "err", err)
		})
		fetches.EachRecord(func(r *kgo.Record) {
			c.process(ctx, r)
			c.cl.MarkCommitRecords(r)
		})
		c.cl.AllowRebalance()
	}
}

func (c *Consumer) process(ctx context.Context, r *kgo.Record) {
	e, err := events.Decode(r.Value)
	if err != nil {
		// A poison record must not block the partition: count it and move on.
		c.log.Warn("skipping invalid event", "partition", r.Partition, "offset", r.Offset, "err", err)
		c.agg.Invalid(ctx, "decode")
		return
	}
	c.agg.Handle(ctx, e)
}

func (c *Consumer) commit() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.cl.CommitMarkedOffsets(ctx); err != nil {
		c.log.Warn("final offset commit failed", "err", err)
	}
}
