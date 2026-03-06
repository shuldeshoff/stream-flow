// Package features implements the two-tier feature store:
//
//   - Online store (Redis) — sub-millisecond access for real-time fraud scoring.
//   - Offline store (ClickHouse) — historical aggregates for analysis and training.
//
// Each feature is named after the entity it belongs to and the aggregation window:
//
//	card:tx_count_1m      — transactions in the last 1 minute for a card
//	card:amount_sum_1h    — transaction amount sum in the last 1 hour
//	customer:countries_24h — unique countries used in the last 24 hours
//	device:card_count     — number of distinct cards seen on a device
package features

import "context"

// FeatureVector is a flat map of feature name → numeric value
// that is passed into the scoring engine for every transaction.
type FeatureVector map[string]float64

// OnlineStore is the low-latency feature read/write interface backed by Redis.
type OnlineStore interface {
	// RecordTransaction updates all card/customer/device sliding-window
	// features atomically for the given transaction context.
	RecordTransaction(ctx context.Context, tc TransactionContext) error

	// GetCardFeatures returns all online features for a card.
	GetCardFeatures(ctx context.Context, cardID string) (FeatureVector, error)

	// GetCustomerFeatures returns all online features for a customer.
	GetCustomerFeatures(ctx context.Context, customerID string) (FeatureVector, error)

	// GetDeviceFeatures returns all online features for a device.
	GetDeviceFeatures(ctx context.Context, deviceID string) (FeatureVector, error)

	// Snapshot returns a merged FeatureVector combining card, customer
	// and device features — ready for the scoring engine.
	Snapshot(ctx context.Context, cardID, customerID, deviceID string) (FeatureVector, error)
}

// OfflineStore is the batch/analytical feature interface backed by ClickHouse.
type OfflineStore interface {
	// GetCardBaselines returns long-window aggregates (7d/30d avg amounts, fraud rate, etc.)
	// that are pre-computed by periodic jobs and stored as simple keys.
	GetCardBaselines(ctx context.Context, cardID string) (FeatureVector, error)

	// GetMerchantRisk returns historical fraud rate and chargeback rate for a merchant.
	GetMerchantRisk(ctx context.Context, merchantID string) (FeatureVector, error)
}

// TransactionContext contains the fields needed to update online features.
type TransactionContext struct {
	CardID     string
	AccountID  string
	CustomerID string
	DeviceID   string
	Amount     float64
	Currency   string
	MerchantID string
	Country    string
	City       string
	IPAddress  string
	Timestamp  int64 // unix milliseconds
}
