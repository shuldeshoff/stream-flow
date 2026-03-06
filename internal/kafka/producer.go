package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publishes events to Kafka topics.
type Producer struct {
	client *kgo.Client
}

// ProducerConfig holds Kafka producer settings.
type ProducerConfig struct {
	Brokers []string
	// ClientID identifies this producer in broker logs.
	ClientID string
}

// NewProducer creates a ready-to-use Kafka producer.
func NewProducer(cfg ProducerConfig) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
		// Idempotent producer: exactly-once delivery at the produce side.
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchMaxBytes(1<<20), // 1 MiB
		kgo.RecordDeliveryTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}

	// Verify broker connectivity.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("kafka producer ping: %w", err)
	}

	log.Info().Strs("brokers", cfg.Brokers).Msg("Kafka producer connected")
	return &Producer{client: client}, nil
}

// Publish serialises value as JSON and produces a record to topic.
// key is used for partition routing (e.g. card_id).
func (p *Producer) Publish(ctx context.Context, topic string, key []byte, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("kafka publish marshal: %w", err)
	}

	record := &kgo.Record{
		Topic: topic,
		Key:   key,
		Value: data,
	}

	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("kafka produce to %s: %w", topic, err)
	}
	return nil
}

// PublishAsync enqueues a record for asynchronous delivery.
// errFn is called if delivery fails; pass nil to ignore errors silently.
func (p *Producer) PublishAsync(topic string, key []byte, value any, errFn func(error)) {
	data, err := json.Marshal(value)
	if err != nil {
		if errFn != nil {
			errFn(fmt.Errorf("kafka async marshal: %w", err))
		}
		return
	}

	record := &kgo.Record{
		Topic: topic,
		Key:   key,
		Value: data,
	}

	p.client.Produce(context.Background(), record, func(r *kgo.Record, err error) {
		if err != nil && errFn != nil {
			errFn(fmt.Errorf("kafka async produce to %s: %w", topic, err))
		}
	})
}

// Close flushes pending records and shuts down the producer.
func (p *Producer) Close() {
	p.client.Close()
}
