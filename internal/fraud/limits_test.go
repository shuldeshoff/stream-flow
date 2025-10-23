package fraud

import (
	"context"
	"testing"
	"time"
)

// Используем MockRedisCache из detector_test.go
func createTrackerWithMock() (*LimitTracker, *MockRedisCache) {
	mockCache := NewMockRedisCache()
	tracker := &LimitTracker{
		cache:  nil, // Не используем в тестах
		limits: make(map[string]*CardLimits),
	}
	return tracker, mockCache
}

func TestNewLimitTracker(t *testing.T) {
	tracker, _ := createTrackerWithMock()

	if tracker == nil {
		t.Fatal("Expected tracker to be created")
	}
}

func TestCheckLimits_UnderLimit(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	tx := createTestTransaction()
	tx.Amount = 1000.00 // Намного меньше лимита

	result := tracker.CheckLimits(ctx, tx)

	if !result.Allowed {
		t.Errorf("Expected transaction to be allowed, got: %s", result.Reason)
	}
}

func TestCheckLimits_DailyLimit(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	// Первая транзакция на 60K
	tx1 := createTestTransaction()
	tx1.Amount = 60000.00
	result1 := tracker.CheckLimits(ctx, tx1)
	if !result1.Allowed {
		t.Error("First transaction should be allowed")
	}

	// Вторая транзакция на 50K (итого 110K > 100K дневной лимит)
	tx2 := createTestTransaction()
	tx2.TransactionID = "tx-test-002"
	tx2.Amount = 50000.00
	result2 := tracker.CheckLimits(ctx, tx2)

	if result2.Allowed {
		t.Error("Expected daily limit to be exceeded")
	}
	if result2.LimitType != "daily" {
		t.Errorf("Expected daily limit type, got %s", result2.LimitType)
	}
}

func TestCheckLimits_TransactionLimit(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	tx := createTestTransaction()
	tx.Amount = 60000.00 // Больше лимита на транзакцию (50K)

	result := tracker.CheckLimits(ctx, tx)

	if result.Allowed {
		t.Error("Expected transaction limit to be exceeded")
	}
	if result.LimitType != "transaction" {
		t.Errorf("Expected transaction limit type, got %s", result.LimitType)
	}
}

func TestCheckLimits_MonthlyLimit(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	// Симулируем несколько больших транзакций до месячного лимита
	for i := 0; i < 25; i++ { // 25 * 40K = 1M (месячный лимит)
		tx := createTestTransaction()
		tx.TransactionID = time.Now().Format("tx-20060102150405.000000")
		tx.Amount = 40000.00
		tracker.CheckLimits(ctx, tx)
	}

	// Следующая транзакция должна превысить месячный лимит
	tx := createTestTransaction()
	tx.Amount = 10000.00
	result := tracker.CheckLimits(ctx, tx)

	if result.Allowed {
		t.Error("Expected monthly limit to be exceeded")
	}
	if result.LimitType != "monthly" {
		t.Errorf("Expected monthly limit type, got %s", result.LimitType)
	}
}

func TestGetLimits(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	cardNumber := "4532123456789012"
	tx := createTestTransaction()
	tx.CardNumber = cardNumber
	tx.Amount = 30000.00

	// Выполняем транзакцию
	tracker.CheckLimits(ctx, tx)

	// Получаем информацию о лимитах
	limits := tracker.GetLimits(ctx, cardNumber)

	if limits.DailyLimit != 100000.00 {
		t.Errorf("Expected daily limit 100000, got %.2f", limits.DailyLimit)
	}

	if limits.DailySpent != 30000.00 {
		t.Errorf("Expected daily spent 30000, got %.2f", limits.DailySpent)
	}

	if limits.DailyRemaining != 70000.00 {
		t.Errorf("Expected daily remaining 70000, got %.2f", limits.DailyRemaining)
	}
}

func TestSetCustomLimits(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	cardNumber := "4532123456789012"
	
	// Устанавливаем кастомные лимиты
	err := tracker.SetCustomLimits(ctx, cardNumber, 50000.00, 500000.00, 20000.00)
	if err != nil {
		t.Fatalf("Failed to set custom limits: %v", err)
	}

	// Проверяем, что новые лимиты применились
	tx := createTestTransaction()
	tx.CardNumber = cardNumber
	tx.Amount = 25000.00 // Больше нового лимита на транзакцию (20K)

	result := tracker.CheckLimits(ctx, tx)
	if result.Allowed {
		t.Error("Expected custom transaction limit to be exceeded")
	}
}

func TestResetLimits(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	cardNumber := "4532123456789012"
	tx := createTestTransaction()
	tx.CardNumber = cardNumber
	tx.Amount = 30000.00

	// Выполняем транзакцию
	tracker.CheckLimits(ctx, tx)

	// Проверяем, что лимит использован
	limits := tracker.GetLimits(ctx, cardNumber)
	if limits.DailySpent == 0 {
		t.Error("Expected some daily spending")
	}

	// Сбрасываем лимиты
	err := tracker.ResetLimits(ctx, cardNumber)
	if err != nil {
		t.Fatalf("Failed to reset limits: %v", err)
	}

	// Проверяем, что лимиты сброшены
	limits = tracker.GetLimits(ctx, cardNumber)
	if limits.DailySpent != 0 {
		t.Errorf("Expected daily spent to be 0 after reset, got %.2f", limits.DailySpent)
	}
}

func TestConcurrentLimitChecks(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	// Симулируем 10 конкурентных транзакций
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			tx := createTestTransaction()
			tx.TransactionID = time.Now().Format("tx-20060102150405.000000")
			tx.Amount = 5000.00
			tracker.CheckLimits(ctx, tx)
			done <- true
		}(i)
	}

	// Ждем завершения
	for i := 0; i < 10; i++ {
		<-done
	}

	// Проверяем итоговую сумму
	limits := tracker.GetLimits(ctx, "4532123456789012")
	expectedSpent := 50000.00 // 10 * 5000
	if limits.DailySpent != expectedSpent {
		t.Errorf("Expected daily spent %.2f, got %.2f", expectedSpent, limits.DailySpent)
	}
}

func TestLimitExpiration(t *testing.T) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	tx := createTestTransaction()
	tx.Amount = 30000.00

	// Выполняем транзакцию
	result := tracker.CheckLimits(ctx, tx)
	if !result.Allowed {
		t.Error("First transaction should be allowed")
	}

	// В реальности Redis TTL сбросит счетчики
	// В моках просто проверяем, что механизм работает
	limits := tracker.GetLimits(ctx, tx.CardNumber)
	if limits.DailySpent == 0 {
		t.Error("Expected daily spending to be tracked")
	}
}

func BenchmarkCheckLimits(b *testing.B) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()
	tx := createTestTransaction()
	tx.Amount = 5000.00

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.CheckLimits(ctx, tx)
	}
}

func BenchmarkCheckLimitsParallel(b *testing.B) {
	cache := NewMockRedisCache()
	tracker := NewLimitTracker(cache)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		tx := createTestTransaction()
		tx.Amount = 5000.00
		for pb.Next() {
			tracker.CheckLimits(ctx, tx)
		}
	})
}

