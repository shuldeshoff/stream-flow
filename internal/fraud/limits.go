package fraud

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/cache"
)

// LimitTracker отслеживает лимиты карт и счетов
type LimitTracker struct {
	cache  *cache.RedisCache
	limits map[string]*CardLimits
	mu     sync.RWMutex
}

type CardLimits struct {
	DailyLimit    float64
	MonthlyLimit  float64
	TransactionLimit float64
	DailySpent    float64
	MonthlySpent  float64
	LastReset     time.Time
}

type LimitCheckResult struct {
	Allowed       bool
	Reason        string
	CurrentSpent  float64
	Limit         float64
	RemainingLimit float64
}

func NewLimitTracker(cache *cache.RedisCache) *LimitTracker {
	return &LimitTracker{
		cache:  cache,
		limits: make(map[string]*CardLimits),
	}
}

// CheckLimits проверяет лимиты перед транзакцией
func (lt *LimitTracker) CheckLimits(ctx context.Context, tx *BankTransaction) *LimitCheckResult {
	// Получаем лимиты карты
	limits := lt.getCardLimits(tx.CardNumber)

	// Проверяем лимит на транзакцию
	if tx.Amount > limits.TransactionLimit {
		return &LimitCheckResult{
			Allowed: false,
			Reason:  "Transaction limit exceeded",
			CurrentSpent: tx.Amount,
			Limit: limits.TransactionLimit,
		}
	}

	// Получаем текущие траты за день
	dailySpent := lt.getDailySpent(ctx, tx.CardNumber)
	if dailySpent + tx.Amount > limits.DailyLimit {
		return &LimitCheckResult{
			Allowed: false,
			Reason: "Daily limit exceeded",
			CurrentSpent: dailySpent,
			Limit: limits.DailyLimit,
			RemainingLimit: limits.DailyLimit - dailySpent,
		}
	}

	// Получаем текущие траты за месяц
	monthlySpent := lt.getMonthlySpent(ctx, tx.CardNumber)
	if monthlySpent + tx.Amount > limits.MonthlyLimit {
		return &LimitCheckResult{
			Allowed: false,
			Reason: "Monthly limit exceeded",
			CurrentSpent: monthlySpent,
			Limit: limits.MonthlyLimit,
			RemainingLimit: limits.MonthlyLimit - monthlySpent,
		}
	}

	return &LimitCheckResult{
		Allowed: true,
		CurrentSpent: dailySpent,
		Limit: limits.DailyLimit,
		RemainingLimit: limits.DailyLimit - dailySpent - tx.Amount,
	}
}

// RecordTransaction записывает транзакцию для отслеживания лимитов
func (lt *LimitTracker) RecordTransaction(ctx context.Context, tx *BankTransaction) error {
	if lt.cache == nil {
		return nil
	}

	// Увеличиваем счетчик дневных трат
	dailyKey := fmt.Sprintf("spend:daily:%s:%s", tx.CardNumber, time.Now().Format("2006-01-02"))
	lt.cache.IncrementBy(ctx, dailyKey, int64(tx.Amount))
	lt.cache.Expire(ctx, dailyKey, 25*time.Hour) // Чуть больше суток

	// Увеличиваем счетчик месячных трат
	monthlyKey := fmt.Sprintf("spend:monthly:%s:%s", tx.CardNumber, time.Now().Format("2006-01"))
	lt.cache.IncrementBy(ctx, monthlyKey, int64(tx.Amount))
	lt.cache.Expire(ctx, monthlyKey, 32*24*time.Hour) // Чуть больше месяца

	log.Debug().
		Str("card", tx.CardNumber).
		Float64("amount", tx.Amount).
		Msg("Transaction recorded for limits")

	return nil
}

func (lt *LimitTracker) getCardLimits(cardNumber string) *CardLimits {
	lt.mu.RLock()
	limits, exists := lt.limits[cardNumber]
	lt.mu.RUnlock()

	if exists {
		return limits
	}

	// Дефолтные лимиты
	defaultLimits := &CardLimits{
		DailyLimit:       100000,  // 100k рублей в день
		MonthlyLimit:     1000000, // 1M рублей в месяц
		TransactionLimit: 50000,   // 50k за транзакцию
		LastReset:        time.Now(),
	}

	lt.mu.Lock()
	lt.limits[cardNumber] = defaultLimits
	lt.mu.Unlock()

	return defaultLimits
}

func (lt *LimitTracker) getDailySpent(ctx context.Context, cardNumber string) float64 {
	if lt.cache == nil {
		return 0
	}

	key := fmt.Sprintf("spend:daily:%s:%s", cardNumber, time.Now().Format("2006-01-02"))
	val, err := lt.cache.Get(ctx, key)
	if err != nil {
		return 0
	}

	var spent int64
	fmt.Sscanf(val, "%d", &spent)
	return float64(spent)
}

func (lt *LimitTracker) getMonthlySpent(ctx context.Context, cardNumber string) float64 {
	if lt.cache == nil {
		return 0
	}

	key := fmt.Sprintf("spend:monthly:%s:%s", cardNumber, time.Now().Format("2006-01"))
	val, err := lt.cache.Get(ctx, key)
	if err != nil {
		return 0
	}

	var spent int64
	fmt.Sscanf(val, "%d", &spent)
	return float64(spent)
}

// SetLimits устанавливает кастомные лимиты для карты
func (lt *LimitTracker) SetLimits(cardNumber string, limits *CardLimits) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.limits[cardNumber] = limits
	
	log.Info().
		Str("card", cardNumber).
		Float64("daily", limits.DailyLimit).
		Float64("monthly", limits.MonthlyLimit).
		Msg("Card limits updated")
}

// GetLimitsStatus возвращает текущий статус лимитов
func (lt *LimitTracker) GetLimitsStatus(ctx context.Context, cardNumber string) map[string]interface{} {
	limits := lt.getCardLimits(cardNumber)
	dailySpent := lt.getDailySpent(ctx, cardNumber)
	monthlySpent := lt.getMonthlySpent(ctx, cardNumber)

	return map[string]interface{}{
		"daily_limit":       limits.DailyLimit,
		"daily_spent":       dailySpent,
		"daily_remaining":   limits.DailyLimit - dailySpent,
		"monthly_limit":     limits.MonthlyLimit,
		"monthly_spent":     monthlySpent,
		"monthly_remaining": limits.MonthlyLimit - monthlySpent,
		"transaction_limit": limits.TransactionLimit,
	}
}

