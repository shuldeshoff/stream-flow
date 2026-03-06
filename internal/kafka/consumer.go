package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

// HandlerFunc processes a single Kafka record.
// Return a non-nil error to prevent offset commit for this record.
type HandlerFunc func(ctx context.Context, topic string, key, value []byte) error

// ConsumerConfig holds consumer group settings.
type ConsumerConfig struct {
	Brokers  []string
	GroupID  string
	Topics   []string
	ClientID string
}

// Consumer wraps a Kafka consumer group with automatic offset management.
// Offsets are committed only after the handler returns nil — giving
// at-least-once delivery with handler-level idempotency as the contract.
type Consumer struct {
	client  *kgo.Client
	handler HandlerFunc
}

// NewConsumer creates a consumer group subscribed to the given topics.
func NewConsumer(cfg ConsumerConfig, handler HandlerFunc) (*Consumer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
		kgo.ConsumerGroup(cfg.GroupID),
		kgo.ConsumeTopics(cfg.Topics...),
		// Start from the earliest offset when the group has no committed offset.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer %s: %w", cfg.GroupID, err)
	}

	log.Info().
		Str("group", cfg.GroupID).
		Strs("topics", cfg.Topics).
		Msg("Kafka consumer created")

	return &Consumer{client: client, handler: handler}, nil
}

// Run starts the fetch–process–commit loop. It blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	for {
		fetches := c.client.PollRecords(ctx, 500)
		if fetches.IsClientClosed() {
			return
		}

		if err := fetches.Err(); err != nil {
			log.Error().Err(err).Msg("Kafka fetch error")
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}

		var commitErr bool
		fetches.EachRecord(func(r *kgo.Record) {
			if commitErr {
				return
			}
			if err := c.handler(ctx, r.Topic, r.Key, r.Value); err != nil {
				log.Error().
					Err(err).
					Str("topic", r.Topic).
					Int64("offset", r.Offset).
					Msg("Handler error — stopping commit for this batch")
				commitErr = true
			}
		})

		if !commitErr {
			if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
				if ctx.Err() == nil {
					log.Error().Err(err).Msg("Kafka offset commit failed")
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// Close shuts down the consumer, committing any pending offsets.
func (c *Consumer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.client.CommitUncommittedOffsets(ctx)
	c.client.Close()
}

// Unmarshal is a convenience helper for handlers.
func Unmarshal(value []byte, dest any) error {
	if err := json.Unmarshal(value, dest); err != nil {
		return fmt.Errorf("kafka unmarshal: %w", err)
	}
	return nil
}
