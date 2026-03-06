package features

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisOnlineStore implements OnlineStore using Redis sorted sets for
// sliding-window counters and Redis sets for uniqueness tracking.
//
// Key schema:
//
//	feat:card:{id}:tx_ts            ZSET  member=txID, score=unix-ms  (all windows derived from this)
//	feat:card:{id}:amount_ts        ZSET  member=unix-ms, score=amount (cumulative sum via scores)
//	feat:card:{id}:merchants_1h     SET   merchantIDs (TTL 1h, rebuilt on access)
//	feat:card:{id}:countries_24h    SET   countries  (TTL 24h)
//	feat:card:{id}:declines_30m     ZSET  timestamps of declines
//	feat:customer:{id}:devices      SET   deviceIDs  (TTL 30d)
//	feat:customer:{id}:countries    SET   countries  (TTL 30d)
//	feat:device:{id}:cards          SET   cardIDs    (TTL 30d)
type RedisOnlineStore struct {
	client *redis.Client
}

// NewRedisOnlineStore creates a feature store backed by an existing Redis client.
func NewRedisOnlineStore(client *redis.Client) *RedisOnlineStore {
	return &RedisOnlineStore{client: client}
}

// RecordTransaction atomically updates all sliding-window features for the
// transaction context. Uses Redis pipelines to minimise round-trips.
func (s *RedisOnlineStore) RecordTransaction(ctx context.Context, tc TransactionContext) error {
	nowMS := float64(tc.Timestamp)
	txKey := fmt.Sprintf("%d", tc.Timestamp) // use unix-ms as ZSET member

	pipe := s.client.Pipeline()

	// --- card features ---
	cardTxKey := fmt.Sprintf("feat:card:%s:tx_ts", tc.CardID)
	pipe.ZAdd(ctx, cardTxKey, redis.Z{Score: nowMS, Member: txKey})
	pipe.Expire(ctx, cardTxKey, 25*time.Hour) // keep 24h + buffer

	cardAmtKey := fmt.Sprintf("feat:card:%s:amount_ts", tc.CardID)
	pipe.ZAdd(ctx, cardAmtKey, redis.Z{Score: tc.Amount, Member: txKey})
	pipe.Expire(ctx, cardAmtKey, 25*time.Hour)

	if tc.MerchantID != "" {
		mKey := fmt.Sprintf("feat:card:%s:merchants_1h", tc.CardID)
		pipe.SAdd(ctx, mKey, tc.MerchantID)
		pipe.Expire(ctx, mKey, time.Hour)
	}

	if tc.Country != "" {
		cKey := fmt.Sprintf("feat:card:%s:countries_24h", tc.CardID)
		pipe.SAdd(ctx, cKey, tc.Country)
		pipe.Expire(ctx, cKey, 24*time.Hour)
	}

	// --- customer features ---
	if tc.CustomerID != "" {
		if tc.DeviceID != "" {
			devKey := fmt.Sprintf("feat:customer:%s:devices", tc.CustomerID)
			pipe.SAdd(ctx, devKey, tc.DeviceID)
			pipe.Expire(ctx, devKey, 30*24*time.Hour)
		}
		if tc.Country != "" {
			custCtryKey := fmt.Sprintf("feat:customer:%s:countries", tc.CustomerID)
			pipe.SAdd(ctx, custCtryKey, tc.Country)
			pipe.Expire(ctx, custCtryKey, 30*24*time.Hour)
		}
	}

	// --- device features ---
	if tc.DeviceID != "" {
		dKey := fmt.Sprintf("feat:device:%s:cards", tc.DeviceID)
		pipe.SAdd(ctx, dKey, tc.CardID)
		pipe.Expire(ctx, dKey, 30*24*time.Hour)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// GetCardFeatures returns all computed online features for a card.
func (s *RedisOnlineStore) GetCardFeatures(ctx context.Context, cardID string) (FeatureVector, error) {
	fv := make(FeatureVector)
	now := float64(time.Now().UnixMilli())

	txKey := fmt.Sprintf("feat:card:%s:tx_ts", cardID)
	amtKey := fmt.Sprintf("feat:card:%s:amount_ts", cardID)

	// Count transactions in sliding windows.
	windows := map[string]float64{
		"card:tx_count_1m":  now - float64(1*time.Minute/time.Millisecond),
		"card:tx_count_5m":  now - float64(5*time.Minute/time.Millisecond),
		"card:tx_count_1h":  now - float64(time.Hour/time.Millisecond),
		"card:tx_count_24h": now - float64(24*time.Hour/time.Millisecond),
	}

	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.IntCmd, len(windows))
	for name, minScore := range windows {
		cmds[name] = pipe.ZCount(ctx, txKey, strconv.FormatFloat(minScore, 'f', 0, 64), "+inf")
	}

	// Amount sums in sliding windows.
	amtCmds := map[string]*redis.StringSliceCmd{}
	for winName, minScore := range map[string]float64{
		"card:amount_sum_1h":  now - float64(time.Hour/time.Millisecond),
		"card:amount_sum_24h": now - float64(24*time.Hour/time.Millisecond),
	} {
		amtCmds[winName] = pipe.ZRangeByScore(ctx, amtKey, &redis.ZRangeBy{
			Min: strconv.FormatFloat(minScore, 'f', 0, 64),
			Max: "+inf",
		})
	}

	// Unique merchants in last 1h.
	mKey := fmt.Sprintf("feat:card:%s:merchants_1h", cardID)
	mCmd := pipe.SCard(ctx, mKey)

	// Unique countries in last 24h.
	cKey := fmt.Sprintf("feat:card:%s:countries_24h", cardID)
	cCmd := pipe.SCard(ctx, cKey)

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("card features pipeline: %w", err)
	}

	for name, cmd := range cmds {
		fv[name] = float64(cmd.Val())
	}

	// Sum amounts by iterating returned scores (stored as member in the zset,
	// but we need the scores — use ZRANGEBYSCORE WITHSCORES instead).
	// For simplicity, store the amounts as a separate list and sum them.
	// The current implementation uses ZRange by score and sums the member-as-amount trick:
	// we store unix-ms as member and amount as score, so we can use ZRangeByScoreWithScores.
	for winName, minScore := range map[string]float64{
		"card:amount_sum_1h":  now - float64(time.Hour/time.Millisecond),
		"card:amount_sum_24h": now - float64(24*time.Hour/time.Millisecond),
	} {
		_ = amtCmds[winName] // already executed above
		zsCmd := s.client.ZRangeByScoreWithScores(ctx, amtKey, &redis.ZRangeBy{
			Min: strconv.FormatFloat(minScore, 'f', 0, 64),
			Max: "+inf",
		})
		var sum float64
		for _, z := range zsCmd.Val() {
			sum += z.Score
		}
		fv[winName] = sum
	}

	fv["card:unique_merchants_1h"] = float64(mCmd.Val())
	fv["card:unique_countries_24h"] = float64(cCmd.Val())

	return fv, nil
}

// GetCustomerFeatures returns features scoped to a customer.
func (s *RedisOnlineStore) GetCustomerFeatures(ctx context.Context, customerID string) (FeatureVector, error) {
	fv := make(FeatureVector)
	if customerID == "" {
		return fv, nil
	}

	pipe := s.client.Pipeline()
	devCmd := pipe.SCard(ctx, fmt.Sprintf("feat:customer:%s:devices", customerID))
	ctryCmd := pipe.SCard(ctx, fmt.Sprintf("feat:customer:%s:countries", customerID))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("customer features pipeline: %w", err)
	}

	fv["customer:device_count"] = float64(devCmd.Val())
	fv["customer:country_count"] = float64(ctryCmd.Val())
	return fv, nil
}

// GetDeviceFeatures returns features scoped to a device.
func (s *RedisOnlineStore) GetDeviceFeatures(ctx context.Context, deviceID string) (FeatureVector, error) {
	fv := make(FeatureVector)
	if deviceID == "" {
		return fv, nil
	}

	cardCount, err := s.client.SCard(ctx, fmt.Sprintf("feat:device:%s:cards", deviceID)).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("device features: %w", err)
	}
	fv["device:card_count"] = float64(cardCount)
	return fv, nil
}

// Snapshot returns a merged FeatureVector for all three entity dimensions.
func (s *RedisOnlineStore) Snapshot(ctx context.Context, cardID, customerID, deviceID string) (FeatureVector, error) {
	fv := make(FeatureVector)

	cardFV, err := s.GetCardFeatures(ctx, cardID)
	if err != nil {
		return nil, err
	}
	for k, v := range cardFV {
		fv[k] = v
	}

	custFV, err := s.GetCustomerFeatures(ctx, customerID)
	if err != nil {
		return nil, err
	}
	for k, v := range custFV {
		fv[k] = v
	}

	devFV, err := s.GetDeviceFeatures(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	for k, v := range devFV {
		fv[k] = v
	}

	return fv, nil
}
