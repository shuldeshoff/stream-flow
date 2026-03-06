package fraud

import (
	"context"
	"testing"
	"time"

	"github.com/shuldeshoff/stream-flow/internal/cache"
)

// Mock Redis Cache для тестирования
type MockRedisCache struct {
	data map[string]interface{}
}

func NewMockRedisCache() *MockRedisCache {
	return &MockRedisCache{
		data: make(map[string]interface{}),
	}
}

func (m *MockRedisCache) Increment(ctx context.Context, key string) (int64, error) {
	val, ok := m.data[key]
	if !ok {
		m.data[key] = int64(1)
		return 1, nil
	}
	count := val.(int64) + 1
	m.data[key] = count
	return count, nil
}

func (m *MockRedisCache) IncrementBy(ctx context.Context, key string, value int64) (int64, error) {
	val, ok := m.data[key]
	if !ok {
		m.data[key] = value
		return value, nil
	}
	count := val.(int64) + value
	m.data[key] = count
	return count, nil
}

func (m *MockRedisCache) Expire(ctx context.Context, key string, duration time.Duration) error {
	return nil
}

func (m *MockRedisCache) Get(ctx context.Context, key string) (string, error) {
	val, ok := m.data[key]
	if !ok {
		return "", nil
	}
	str, ok := val.(string)
	if !ok {
		return "", nil
	}
	return str, nil
}

func (m *MockRedisCache) Set(ctx context.Context, key string, value interface{}, duration time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *MockRedisCache) SetJSON(ctx context.Context, key string, value interface{}, duration time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *MockRedisCache) GetJSON(ctx context.Context, key string, dest interface{}) error {
	val, ok := m.data[key]
	if !ok {
		return nil
	}
	// Simple mock implementation
	return nil
}

func (m *MockRedisCache) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		delete(m.data, key)
	}
	return nil
}

func (m *MockRedisCache) GetEventTypeStats(ctx context.Context, eventType string, window time.Duration) (int64, error) {
	key := "stats:type:" + eventType
	val, ok := m.data[key]
	if !ok {
		return 0, nil
	}
	count, _ := val.(int64)
	return count, nil
}

func (m *MockRedisCache) IncrementEventTypeStats(ctx context.Context, eventType string, window time.Duration) error {
	key := "stats:type:" + eventType
	val, ok := m.data[key]
	if !ok {
		m.data[key] = int64(1)
		return nil
	}
	count := val.(int64) + 1
	m.data[key] = count
	return nil
}

func (m *MockRedisCache) GetSourceStats(ctx context.Context, source string, window time.Duration) (int64, error) {
	key := "stats:source:" + source
	val, ok := m.data[key]
	if !ok {
		return 0, nil
	}
	count, _ := val.(int64)
	return count, nil
}

func (m *MockRedisCache) IncrementSourceStats(ctx context.Context, source string, window time.Duration) error {
	key := "stats:source:" + source
	val, ok := m.data[key]
	if !ok {
		m.data[key] = int64(1)
		return nil
	}
	count := val.(int64) + 1
	m.data[key] = count
	return nil
}

func (m *MockRedisCache) GetAllEventTypeStats(ctx context.Context, window time.Duration) (map[string]int64, error) {
	stats := make(map[string]int64)
	for key, val := range m.data {
		if count, ok := val.(int64); ok {
			stats[key] = count
		}
	}
	return stats, nil
}

func (m *MockRedisCache) Close() error {
	return nil
}

// Helper для создания тестовой транзакции
func createTestTransaction() *BankTransaction {
	return &BankTransaction{
		TransactionID: "tx-test-001",
		CardNumber:    "4532123456789012",
		Amount:        5000.00,
		Currency:      "RUB",
		MerchantID:    "merchant-001",
		MerchantName:  "Test Shop",
		MerchantMCC:   "5411", // Grocery stores
		Timestamp:     time.Now(),
		IPAddress:     "192.168.1.1",
		DeviceID:      "device-001",
		Location: GeoLocation{
			Country: "RU",
			City:    "Moscow",
			Lat:     55.7558,
			Lon:     37.6173,
		},
		CardType:  "debit",
		AccountID: "acc-001",
		UserID:    "user-001",
	}
}

// Wrapper для использования мока в тестах
func createDetectorWithMock() (*FraudDetector, *MockRedisCache) {
	mockCache := NewMockRedisCache()
	// Для тестов создаем детектор напрямую без правил
	detector := &FraudDetector{
		cache:     nil, // Не используем в тестах
		rules:     make([]Rule, 0),
		blocklist: make(map[string]time.Time),
	}
	// Добавляем правила с моковым кэшем
	detector.AddRule(&VelocityRule{cache: mockCache})
	detector.AddRule(&LocationAnomalyRule{cache: mockCache})
	detector.AddRule(&AmountAnomalyRule{cache: mockCache})
	detector.AddRule(&MultiDeviceRule{cache: mockCache})
	detector.AddRule(&UnusualTimeRule{})
	detector.AddRule(&HighRiskMerchantRule{})
	
	return detector, mockCache
}

func TestNewFraudDetector(t *testing.T) {
	detector, _ := createDetectorWithMock()

	if detector == nil {
		t.Fatal("Expected detector to be created")
	}

	if len(detector.rules) == 0 {
		t.Error("Expected rules to be initialized")
	}
}

func TestVelocityRule_Normal(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()

	// Первая транзакция - должна пройти
	result := detector.CheckTransaction(ctx, tx)
	if result.IsFraud {
		t.Errorf("Expected transaction to be allowed, got fraud: %s", result.Reason)
	}
}

func TestVelocityRule_Attack(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()

	// Симулируем 6 транзакций за минуту (порог = 5)
	for i := 0; i < 6; i++ {
		tx.TransactionID = time.Now().Format("tx-20060102150405.000000")
		result := detector.CheckTransaction(ctx, tx)
		
		if i < 5 {
			if result.IsFraud {
				t.Errorf("Transaction %d should be allowed", i+1)
			}
		} else {
			// 6-я транзакция должна быть заблокирована
			if !result.IsFraud {
				t.Error("Expected fraud detection for velocity attack")
			}
			if result.TriggeredRule != "velocity_check" {
				t.Errorf("Expected velocity_check rule, got %s", result.TriggeredRule)
			}
		}
	}
}

func TestAmountAnomalyRule_Normal(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()
	tx.Amount = 5000.00 // Нормальная сумма

	result := detector.CheckTransaction(ctx, tx)
	if result.IsFraud {
		t.Errorf("Expected normal amount to be allowed, got: %s", result.Reason)
	}
}

func TestAmountAnomalyRule_HighAmount(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()
	tx.Amount = 150000.00 // Очень высокая сумма (> 100K)

	result := detector.CheckTransaction(ctx, tx)
	if !result.IsFraud {
		t.Error("Expected fraud detection for high amount")
	}
	if result.TriggeredRule != "amount_anomaly" {
		t.Errorf("Expected amount_anomaly rule, got %s", result.TriggeredRule)
	}
	if result.Confidence < 0.7 {
		t.Errorf("Expected high confidence, got %.2f", result.Confidence)
	}
}

func TestLocationAnomalyRule_SameLocation(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx1 := createTestTransaction()
	tx1.Location = GeoLocation{Country: "RU", City: "Moscow"}
	
	// Первая транзакция
	detector.CheckTransaction(ctx, tx1)

	// Вторая транзакция в том же городе
	tx2 := createTestTransaction()
	tx2.TransactionID = "tx-test-002"
	tx2.Location = GeoLocation{Country: "RU", City: "Moscow"}

	result := detector.CheckTransaction(ctx, tx2)
	if result.IsFraud {
		t.Errorf("Expected same location to be allowed, got: %s", result.Reason)
	}
}

func TestLocationAnomalyRule_ImpossibleTravel(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	// Транзакция в Москве
	tx1 := createTestTransaction()
	tx1.Location = GeoLocation{Country: "RU", City: "Moscow"}
	tx1.Timestamp = time.Now().Add(-5 * time.Minute)
	detector.CheckTransaction(ctx, tx1)

	// Через 5 минут транзакция в США (физически невозможно)
	tx2 := createTestTransaction()
	tx2.TransactionID = "tx-test-002"
	tx2.Location = GeoLocation{Country: "US", City: "New York"}
	tx2.Timestamp = time.Now()

	result := detector.CheckTransaction(ctx, tx2)
	if !result.IsFraud {
		t.Error("Expected fraud detection for impossible travel")
	}
	if result.TriggeredRule != "location_anomaly" {
		t.Errorf("Expected location_anomaly rule, got %s", result.TriggeredRule)
	}
}

func TestUnusualTimeRule_DayTime(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()
	// Устанавливаем дневное время (14:00)
	tx.Timestamp = time.Date(2025, 10, 23, 14, 0, 0, 0, time.UTC)

	result := detector.CheckTransaction(ctx, tx)
	if result.IsFraud && result.TriggeredRule == "unusual_time" {
		t.Error("Expected daytime transaction to be allowed")
	}
}

func TestUnusualTimeRule_NightTime(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()
	// Устанавливаем ночное время (03:00)
	tx.Timestamp = time.Date(2025, 10, 23, 3, 0, 0, 0, time.UTC)

	result := detector.CheckTransaction(ctx, tx)
	if !result.IsFraud {
		t.Error("Expected fraud detection for unusual time")
	}
	if result.TriggeredRule != "unusual_time" {
		t.Errorf("Expected unusual_time rule, got %s", result.TriggeredRule)
	}
}

func TestHighRiskMerchantRule_Normal(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()
	tx.MerchantMCC = "5411" // Grocery store - low risk

	result := detector.CheckTransaction(ctx, tx)
	if result.IsFraud && result.TriggeredRule == "high_risk_merchant" {
		t.Error("Expected low-risk merchant to be allowed")
	}
}

func TestHighRiskMerchantRule_Gambling(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx := createTestTransaction()
	tx.MerchantMCC = "7995" // Gambling - high risk
	tx.Amount = 10000.00

	result := detector.CheckTransaction(ctx, tx)
	if !result.IsFraud {
		t.Error("Expected fraud detection for high-risk merchant")
	}
	if result.TriggeredRule != "high_risk_merchant" {
		t.Errorf("Expected high_risk_merchant rule, got %s", result.TriggeredRule)
	}
}

func TestMultiDeviceRule_SameDevice(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	tx1 := createTestTransaction()
	tx1.DeviceID = "device-001"
	detector.CheckTransaction(ctx, tx1)

	tx2 := createTestTransaction()
	tx2.TransactionID = "tx-test-002"
	tx2.DeviceID = "device-001" // Same device

	result := detector.CheckTransaction(ctx, tx2)
	if result.IsFraud && result.TriggeredRule == "multi_device" {
		t.Error("Expected same device to be allowed")
	}
}

func TestBlockCard(t *testing.T) {
	detector, _ := createDetectorWithMock()

	cardNumber := "4532123456789012"
	reason := "Test block"

	detector.BlockCard(cardNumber, reason)

	if !detector.isBlocked(cardNumber) {
		t.Error("Expected card to be blocked")
	}

	// Проверяем, что заблокированная карта не проходит проверку
	ctx := context.Background()
	tx := createTestTransaction()
	tx.CardNumber = cardNumber

	result := detector.CheckTransaction(ctx, tx)
	if !result.IsFraud {
		t.Error("Expected blocked card to be detected as fraud")
	}
	if result.Reason != "Card is blocked" {
		t.Errorf("Expected 'Card is blocked', got %s", result.Reason)
	}
}

func TestUnblockCard(t *testing.T) {
	detector, _ := createDetectorWithMock()

	cardNumber := "4532123456789012"
	
	// Блокируем
	detector.BlockCard(cardNumber, "Test")
	if !detector.isBlocked(cardNumber) {
		t.Error("Card should be blocked")
	}

	// Разблокируем
	detector.UnblockCard(cardNumber)
	if detector.isBlocked(cardNumber) {
		t.Error("Card should be unblocked")
	}
}

func TestGetStats(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	// Выполняем несколько проверок
	tx1 := createTestTransaction()
	detector.CheckTransaction(ctx, tx1)

	tx2 := createTestTransaction()
	tx2.TransactionID = "tx-test-002"
	tx2.Amount = 150000.00 // Fraud
	detector.CheckTransaction(ctx, tx2)

	stats := detector.GetStats()

	if stats.TotalChecked != 2 {
		t.Errorf("Expected 2 total checks, got %d", stats.TotalChecked)
	}

	if stats.FraudDetected < 1 {
		t.Errorf("Expected at least 1 fraud detected, got %d", stats.FraudDetected)
	}
}

func TestConcurrentTransactions(t *testing.T) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	// Симулируем конкурентные транзакции
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			tx := createTestTransaction()
			tx.TransactionID = time.Now().Format("tx-20060102150405.000000")
			detector.CheckTransaction(ctx, tx)
			done <- true
		}(i)
	}

	// Ждем завершения всех горутин
	for i := 0; i < 10; i++ {
		<-done
	}

	stats := detector.GetStats()
	if stats.TotalChecked != 10 {
		t.Errorf("Expected 10 concurrent transactions, got %d", stats.TotalChecked)
	}
}

func BenchmarkCheckTransaction(b *testing.B) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()
	tx := createTestTransaction()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.CheckTransaction(ctx, tx)
	}
}

func BenchmarkCheckTransactionParallel(b *testing.B) {
	detector, _ := createDetectorWithMock()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		tx := createTestTransaction()
		for pb.Next() {
			detector.CheckTransaction(ctx, tx)
		}
	})
}

