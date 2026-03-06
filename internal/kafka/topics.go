package kafka

// Topic names for the event pipeline.
// Events flow through stages: raw → validated → enriched → scored → decisions.
const (
	TopicTransactionsRaw       = "transactions.raw"
	TopicTransactionsValidated = "transactions.validated"
	TopicTransactionsEnriched  = "transactions.enriched"
	TopicTransactionsScored    = "transactions.scored"
	TopicTransactionsDecisions = "transactions.decisions"
	TopicTransactionsDLQ       = "transactions.dlq"
	TopicTransactionsRetry1m   = "transactions.retry.1m"
	TopicTransactionsRetry5m   = "transactions.retry.5m"

	TopicEventsRaw = "events.raw"
	TopicEventsDLQ = "events.dlq"

	TopicCardsEvents  = "cards.events"
	TopicLimitsEvents = "limits.events"
)

// PartitionKey returns the Kafka partition key for a transaction.
// Using card_id ensures all events for one card go to the same partition,
// which is required for correct ordering in velocity and sequence checks.
func PartitionKey(cardID, accountID string) []byte {
	if cardID != "" {
		return []byte(cardID)
	}
	return []byte(accountID)
}

// DefaultTopicPartitions is the recommended partition count per topic.
// More partitions = more parallelism for consumer groups.
const DefaultTopicPartitions = 12
