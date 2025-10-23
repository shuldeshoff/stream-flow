package fraud

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/cache"
)

// FraudDetector детектирует мошенничество в банковских транзакциях
type FraudDetector struct {
	cache    *cache.RedisCache
	rules    []Rule
	mu       sync.RWMutex
	stats    FraudStats
	blocklist map[string]time.Time // заблокированные карты
	blockMu   sync.RWMutex
}

type FraudStats struct {
	TotalChecked     int64
	FraudDetected    int64
	FalsePositives   int64
	CardsBlocked     int64
	mu               sync.RWMutex
}

type FraudResult struct {
	IsFraud       bool
	Confidence    float64 // 0.0 - 1.0
	Reason        string
	TriggeredRule string
	Action        Action
	Details       map[string]interface{}
}

type Action string

const (
	ActionAllow  Action = "allow"
	ActionBlock  Action = "block"
	ActionReview Action = "review"
	ActionAlert  Action = "alert"
)

// Rule интерфейс для fraud detection правил
type Rule interface {
	Check(ctx context.Context, transaction *BankTransaction) (*FraudResult, error)
	Name() string
	Priority() int
}

// BankTransaction представляет банковскую транзакцию
type BankTransaction struct {
	TransactionID string
	CardNumber    string  // last 4 digits
	Amount        float64
	Currency      string
	MerchantID    string
	MerchantName  string
	MerchantMCC   string // Merchant Category Code
	Timestamp     time.Time
	IPAddress     string
	DeviceID      string
	Location      GeoLocation
	CardType      string // debit/credit
	AccountID     string
	UserID        string
}

type GeoLocation struct {
	Country string
	City    string
	Lat     float64
	Lon     float64
}

func NewFraudDetector(cache *cache.RedisCache) *FraudDetector {
	fd := &FraudDetector{
		cache:     cache,
		rules:     make([]Rule, 0),
		blocklist: make(map[string]time.Time),
	}

	// Добавляем стандартные правила
	fd.AddRule(&VelocityRule{cache: cache})
	fd.AddRule(&LocationAnomalyRule{cache: cache})
	fd.AddRule(&AmountAnomalyRule{cache: cache})
	fd.AddRule(&MultiDeviceRule{cache: cache})
	fd.AddRule(&UnusualTimeRule{})
	fd.AddRule(&BlacklistRule{cache: cache})
	fd.AddRule(&HighRiskMerchantRule{})

	return fd
}

func (fd *FraudDetector) AddRule(rule Rule) {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	fd.rules = append(fd.rules, rule)
	log.Info().Str("rule", rule.Name()).Int("priority", rule.Priority()).Msg("Fraud rule added")
}

// CheckTransaction проверяет транзакцию на мошенничество
func (fd *FraudDetector) CheckTransaction(ctx context.Context, tx *BankTransaction) *FraudResult {
	fd.stats.mu.Lock()
	fd.stats.TotalChecked++
	fd.stats.mu.Unlock()

	// Проверяем blocklist
	if fd.isBlocked(tx.CardNumber) {
		return &FraudResult{
			IsFraud:       true,
			Confidence:    1.0,
			Reason:        "Card is blocked",
			TriggeredRule: "blocklist",
			Action:        ActionBlock,
		}
	}

	// Запускаем правила по приоритету
	fd.mu.RLock()
	rules := fd.rules
	fd.mu.RUnlock()

	var highestConfidence float64
	var fraudResult *FraudResult

	for _, rule := range rules {
		result, err := rule.Check(ctx, tx)
		if err != nil {
			log.Error().Err(err).Str("rule", rule.Name()).Msg("Rule check failed")
			continue
		}

		if result != nil && result.IsFraud && result.Confidence > highestConfidence {
			highestConfidence = result.Confidence
			fraudResult = result
		}
	}

	if fraudResult != nil && fraudResult.IsFraud {
		fd.stats.mu.Lock()
		fd.stats.FraudDetected++
		fd.stats.mu.Unlock()

		log.Warn().
			Str("transaction_id", tx.TransactionID).
			Str("card", tx.CardNumber).
			Float64("confidence", fraudResult.Confidence).
			Str("reason", fraudResult.Reason).
			Msg("Fraud detected")

		// Автоматическая блокировка при высокой уверенности
		if fraudResult.Confidence >= 0.9 && fraudResult.Action == ActionBlock {
			fd.BlockCard(tx.CardNumber, "Fraud detected: "+fraudResult.Reason)
		}

		return fraudResult
	}

	return &FraudResult{
		IsFraud:    false,
		Confidence: 0.0,
		Action:     ActionAllow,
	}
}

// BlockCard блокирует карту
func (fd *FraudDetector) BlockCard(cardNumber, reason string) {
	fd.blockMu.Lock()
	defer fd.blockMu.Unlock()

	fd.blocklist[cardNumber] = time.Now()
	
	fd.stats.mu.Lock()
	fd.stats.CardsBlocked++
	fd.stats.mu.Unlock()

	// Сохраняем в Redis
	if fd.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		
		key := fmt.Sprintf("blocked_card:%s", cardNumber)
		fd.cache.Set(ctx, key, reason, 24*time.Hour)
	}

	log.Warn().
		Str("card", cardNumber).
		Str("reason", reason).
		Msg("Card blocked")
}

func (fd *FraudDetector) isBlocked(cardNumber string) bool {
	fd.blockMu.RLock()
	_, blocked := fd.blocklist[cardNumber]
	fd.blockMu.RUnlock()

	if blocked {
		return true
	}

	// Проверяем Redis
	if fd.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		
		key := fmt.Sprintf("blocked_card:%s", cardNumber)
		_, err := fd.cache.Get(ctx, key)
		return err == nil
	}

	return false
}

func (fd *FraudDetector) GetStats() FraudStats {
	fd.stats.mu.RLock()
	defer fd.stats.mu.RUnlock()
	
	return FraudStats{
		TotalChecked:   fd.stats.TotalChecked,
		FraudDetected:  fd.stats.FraudDetected,
		CardsBlocked:   fd.stats.CardsBlocked,
	}
}

// --- Fraud Detection Rules ---

// VelocityRule детектирует много транзакций за короткое время
type VelocityRule struct {
	cache *cache.RedisCache
}

func (r *VelocityRule) Name() string { return "velocity_check" }
func (r *VelocityRule) Priority() int { return 10 }

func (r *VelocityRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	if r.cache == nil {
		return nil, nil
	}

	// Считаем транзакции за последнюю минуту
	key := fmt.Sprintf("tx_count:%s:1m", tx.CardNumber)
	count, _ := r.cache.Increment(ctx, key)
	r.cache.Expire(ctx, key, 1*time.Minute)

	// Более 5 транзакций за минуту - подозрительно
	if count > 5 {
		return &FraudResult{
			IsFraud:       true,
			Confidence:    0.85,
			Reason:        fmt.Sprintf("%d transactions in 1 minute", count),
			TriggeredRule: r.Name(),
			Action:        ActionBlock,
			Details: map[string]interface{}{
				"transaction_count": count,
				"window":            "1m",
			},
		}, nil
	}

	return nil, nil
}

// LocationAnomalyRule детектирует невозможные перемещения
type LocationAnomalyRule struct {
	cache *cache.RedisCache
}

func (r *LocationAnomalyRule) Name() string { return "location_anomaly" }
func (r *LocationAnomalyRule) Priority() int { return 9 }

func (r *LocationAnomalyRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	if r.cache == nil {
		return nil, nil
	}

	// Получаем последнюю локацию
	key := fmt.Sprintf("last_location:%s", tx.CardNumber)
	lastLoc, err := r.cache.Get(ctx, key)
	
	// Сохраняем текущую локацию
	currentLoc := fmt.Sprintf("%s:%s", tx.Location.Country, tx.Location.City)
	r.cache.Set(ctx, key, currentLoc, 1*time.Hour)

	if err == nil && lastLoc != "" {
		// Если страна изменилась за короткое время - подозрительно
		if lastLoc != currentLoc {
			// В реальности здесь был бы расчет расстояния и времени
			return &FraudResult{
				IsFraud:       true,
				Confidence:    0.75,
				Reason:        "Location changed too quickly",
				TriggeredRule: r.Name(),
				Action:        ActionReview,
				Details: map[string]interface{}{
					"previous_location": lastLoc,
					"current_location":  currentLoc,
				},
			}, nil
		}
	}

	return nil, nil
}

// AmountAnomalyRule детектирует необычные суммы
type AmountAnomalyRule struct {
	cache *cache.RedisCache
}

func (r *AmountAnomalyRule) Name() string { return "amount_anomaly" }
func (r *AmountAnomalyRule) Priority() int { return 7 }

func (r *AmountAnomalyRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	// Транзакции > 100000 рублей требуют проверки
	if tx.Amount > 100000 {
		return &FraudResult{
			IsFraud:       true,
			Confidence:    0.60,
			Reason:        "Unusually high amount",
			TriggeredRule: r.Name(),
			Action:        ActionReview,
			Details: map[string]interface{}{
				"amount": tx.Amount,
			},
		}, nil
	}

	return nil, nil
}

// MultiDeviceRule детектирует использование разных устройств
type MultiDeviceRule struct {
	cache *cache.RedisCache
}

func (r *MultiDeviceRule) Name() string { return "multi_device" }
func (r *MultiDeviceRule) Priority() int { return 8 }

func (r *MultiDeviceRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	if r.cache == nil || tx.DeviceID == "" {
		return nil, nil
	}

	// Считаем уникальные устройства за час
	key := fmt.Sprintf("devices:%s", tx.CardNumber)
	deviceKey := fmt.Sprintf("%s:%s", key, tx.DeviceID)
	
	r.cache.Set(ctx, deviceKey, "1", 1*time.Hour)
	
	// В реальности здесь был бы SCARD для подсчета уникальных устройств
	// Упрощенная версия: если device_id меняется часто - подозрительно
	
	return nil, nil
}

// UnusualTimeRule детектирует транзакции в необычное время
type UnusualTimeRule struct{}

func (r *UnusualTimeRule) Name() string { return "unusual_time" }
func (r *UnusualTimeRule) Priority() int { return 5 }

func (r *UnusualTimeRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	hour := tx.Timestamp.Hour()
	
	// Транзакции с 2 до 5 утра - подозрительны
	if hour >= 2 && hour < 5 {
		return &FraudResult{
			IsFraud:       true,
			Confidence:    0.40,
			Reason:        "Transaction at unusual time",
			TriggeredRule: r.Name(),
			Action:        ActionAlert,
			Details: map[string]interface{}{
				"hour": hour,
			},
		}, nil
	}

	return nil, nil
}

// BlacklistRule проверяет черные списки
type BlacklistRule struct {
	cache *cache.RedisCache
}

func (r *BlacklistRule) Name() string { return "blacklist" }
func (r *BlacklistRule) Priority() int { return 10 }

func (r *BlacklistRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	if r.cache == nil {
		return nil, nil
	}

	// Проверяем IP в blacklist
	key := fmt.Sprintf("blacklist:ip:%s", tx.IPAddress)
	if _, err := r.cache.Get(ctx, key); err == nil {
		return &FraudResult{
			IsFraud:       true,
			Confidence:    0.95,
			Reason:        "IP address in blacklist",
			TriggeredRule: r.Name(),
			Action:        ActionBlock,
		}, nil
	}

	return nil, nil
}

// HighRiskMerchantRule проверяет рискованных мерчантов
type HighRiskMerchantRule struct{}

func (r *HighRiskMerchantRule) Name() string { return "high_risk_merchant" }
func (r *HighRiskMerchantRule) Priority() int { return 6 }

func (r *HighRiskMerchantRule) Check(ctx context.Context, tx *BankTransaction) (*FraudResult, error) {
	// MCC коды высокого риска (например, онлайн-казино, криптовалюты)
	highRiskMCC := map[string]bool{
		"7995": true, // Gambling
		"6051": true, // Crypto
	}

	if highRiskMCC[tx.MerchantMCC] {
		return &FraudResult{
			IsFraud:       true,
			Confidence:    0.50,
			Reason:        "High-risk merchant category",
			TriggeredRule: r.Name(),
			Action:        ActionReview,
			Details: map[string]interface{}{
				"mcc": tx.MerchantMCC,
			},
		}, nil
	}

	return nil, nil
}

