package features

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ClickHouseOfflineStore implements OfflineStore against the events table
// that the storage layer already populates.
type ClickHouseOfflineStore struct {
	db driver.Conn
}

// NewClickHouseOfflineStore wraps an existing ClickHouse connection.
func NewClickHouseOfflineStore(db driver.Conn) *ClickHouseOfflineStore {
	return &ClickHouseOfflineStore{db: db}
}

// GetCardBaselines queries 30-day aggregate baselines for a card.
// These are used as reference values in the scoring engine to detect
// deviations from the card's historical behaviour.
func (s *ClickHouseOfflineStore) GetCardBaselines(ctx context.Context, cardID string) (FeatureVector, error) {
	fv := make(FeatureVector)

	const q = `
		SELECT
			countIf(toDate(timestamp) >= today() - 30)                 AS tx_count_30d,
			sumIf(JSONExtractFloat(data, 'amount'),
			      toDate(timestamp) >= today() - 30)                   AS amount_sum_30d,
			avgIf(JSONExtractFloat(data, 'amount'),
			      toDate(timestamp) >= today() - 30)                   AS amount_avg_30d,
			countIf(toDate(timestamp) >= today() - 7)                  AS tx_count_7d,
			uniqIf(JSONExtractString(data, 'merchant_id'),
			       toDate(timestamp) >= today() - 30)                  AS unique_merchants_30d
		FROM events
		WHERE type = 'transaction'
		  AND JSONExtractString(data, 'card_id') = ?
		LIMIT 1
	`

	row := s.db.QueryRow(ctx, q, cardID)

	var (
		txCount30d       float64
		amountSum30d     float64
		amountAvg30d     float64
		txCount7d        float64
		uniqueMerch30d   float64
	)

	if err := row.Scan(&txCount30d, &amountSum30d, &amountAvg30d, &txCount7d, &uniqueMerch30d); err != nil {
		return nil, fmt.Errorf("card baselines query: %w", err)
	}

	fv["card:tx_count_30d"] = txCount30d
	fv["card:amount_sum_30d"] = amountSum30d
	fv["card:amount_avg_30d"] = amountAvg30d
	fv["card:tx_count_7d"] = txCount7d
	fv["card:unique_merchants_30d"] = uniqueMerch30d

	return fv, nil
}

// GetMerchantRisk returns pre-computed risk indicators for a merchant.
// A periodic background job should materialise these from the events table.
func (s *ClickHouseOfflineStore) GetMerchantRisk(ctx context.Context, merchantID string) (FeatureVector, error) {
	fv := make(FeatureVector)

	const q = `
		SELECT
			countIf(toDate(timestamp) >= today() - 30)                 AS tx_count_30d,
			countIf(JSONExtractString(data, 'fraud_label') = 'confirmed'
			        AND toDate(timestamp) >= today() - 30)              AS fraud_count_30d
		FROM events
		WHERE type = 'transaction'
		  AND JSONExtractString(data, 'merchant_id') = ?
		LIMIT 1
	`

	row := s.db.QueryRow(ctx, q, merchantID)

	var total, fraudCount float64
	if err := row.Scan(&total, &fraudCount); err != nil {
		return nil, fmt.Errorf("merchant risk query: %w", err)
	}

	fv["merchant:tx_count_30d"] = total
	fv["merchant:fraud_count_30d"] = fraudCount
	if total > 0 {
		fv["merchant:fraud_rate_30d"] = fraudCount / total
	}

	return fv, nil
}
