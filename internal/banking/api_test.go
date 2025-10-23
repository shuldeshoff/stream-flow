package banking

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sul/streamflow/internal/fraud"
)

// Mock для тестирования
type MockFraudDetector struct {
	shouldDetectFraud bool
	blockedCards      map[string]bool
}

func NewMockFraudDetector() *MockFraudDetector {
	return &MockFraudDetector{
		blockedCards: make(map[string]bool),
	}
}

func (m *MockFraudDetector) CheckTransaction(ctx context.Context, tx *fraud.BankTransaction) *fraud.FraudResult {
	if m.shouldDetectFraud {
		return &fraud.FraudResult{
			IsFraud:       true,
			Confidence:    0.95,
			Reason:        "Mock fraud detected",
			TriggeredRule: "test_rule",
			Action:        fraud.ActionBlock,
		}
	}
	return &fraud.FraudResult{
		IsFraud:    false,
		Confidence: 0.0,
		Action:     fraud.ActionAllow,
	}
}

func (m *MockFraudDetector) BlockCard(cardNumber, reason string) {
	m.blockedCards[cardNumber] = true
}

func (m *MockFraudDetector) UnblockCard(cardNumber string) {
	delete(m.blockedCards, cardNumber)
}

func (m *MockFraudDetector) isBlocked(cardNumber string) bool {
	return m.blockedCards[cardNumber]
}

func (m *MockFraudDetector) GetStats() fraud.FraudStats {
	return fraud.FraudStats{}
}

type MockLimitTracker struct {
	shouldExceedLimit bool
}

func NewMockLimitTracker() *MockLimitTracker {
	return &MockLimitTracker{}
}

func (m *MockLimitTracker) CheckLimits(ctx context.Context, tx *fraud.BankTransaction) *fraud.LimitCheckResult {
	if m.shouldExceedLimit {
		return &fraud.LimitCheckResult{
			Allowed:       false,
			LimitType:     "daily",
			LimitValue:    100000.00,
			CurrentValue:  95000.00,
			AttemptedValue: tx.Amount,
			Reason:        "Daily limit exceeded",
		}
	}
	return &fraud.LimitCheckResult{
		Allowed:       true,
		LimitType:     "",
		LimitValue:    100000.00,
		CurrentValue:  10000.00,
		AttemptedValue: tx.Amount,
	}
}

func (m *MockLimitTracker) GetLimits(ctx context.Context, cardNumber string) *fraud.CardLimits {
	return &fraud.CardLimits{
		CardNumber:       cardNumber,
		DailyLimit:       100000.00,
		DailySpent:       10000.00,
		DailyRemaining:   90000.00,
		MonthlyLimit:     1000000.00,
		MonthlySpent:     50000.00,
		MonthlyRemaining: 950000.00,
		TransactionLimit: 50000.00,
	}
}

func (m *MockLimitTracker) SetCustomLimits(ctx context.Context, cardNumber string, daily, monthly, transaction float64) error {
	return nil
}

func (m *MockLimitTracker) ResetLimits(ctx context.Context, cardNumber string) error {
	return nil
}

func TestProcessTransaction_Success(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	reqBody := map[string]interface{}{
		"card_number": "4532123456789012",
		"amount":      5000.00,
		"currency":    "RUB",
		"merchant":    "Test Shop",
		"location":    "Moscow",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/banking/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.handleTransaction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)

	if response["status"] != "approved" {
		t.Errorf("Expected status approved, got %v", response["status"])
	}
}

func TestProcessTransaction_FraudDetected(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	fraudDetector.shouldDetectFraud = true
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	reqBody := map[string]interface{}{
		"card_number": "4532123456789012",
		"amount":      150000.00,
		"currency":    "RUB",
		"merchant":    "Suspicious Shop",
		"location":    "Unknown",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/banking/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.handleTransaction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)

	if response["status"] != "declined" {
		t.Errorf("Expected status declined, got %v", response["status"])
	}

	if response["reason"] == "" {
		t.Error("Expected fraud reason in response")
	}
}

func TestProcessTransaction_LimitExceeded(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	limitTracker.shouldExceedLimit = true
	api := NewBankingAPI(fraudDetector, limitTracker)

	reqBody := map[string]interface{}{
		"card_number": "4532123456789012",
		"amount":      60000.00,
		"currency":    "RUB",
		"merchant":    "Expensive Shop",
		"location":    "Moscow",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/banking/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.handleTransaction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)

	if response["status"] != "declined" {
		t.Errorf("Expected status declined, got %v", response["status"])
	}

	if response["reason"] == "" {
		t.Error("Expected limit exceeded reason in response")
	}
}

func TestGetCardLimits_Success(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	req := httptest.NewRequest(http.MethodGet, "/api/banking/cards/4532123456789012/limits", nil)
	w := httptest.NewRecorder()

	api.handleGetLimits(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response fraud.CardLimits
	json.NewDecoder(w.Body).Decode(&response)

	if response.CardNumber != "4532123456789012" {
		t.Errorf("Expected card number 4532123456789012, got %s", response.CardNumber)
	}

	if response.DailyLimit == 0 {
		t.Error("Expected daily limit to be set")
	}
}

func TestBlockCard_Success(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	req := httptest.NewRequest(http.MethodPost, "/api/banking/cards/4532123456789012/block", nil)
	w := httptest.NewRecorder()

	api.handleBlockCard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)

	if response["status"] != "blocked" {
		t.Errorf("Expected status blocked, got %v", response["status"])
	}

	if !fraudDetector.isBlocked("4532123456789012") {
		t.Error("Expected card to be blocked in fraud detector")
	}
}

func TestUnblockCard_Success(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	// Сначала блокируем
	fraudDetector.BlockCard("4532123456789012", "Test")

	req := httptest.NewRequest(http.MethodPost, "/api/banking/cards/4532123456789012/unblock", nil)
	w := httptest.NewRecorder()

	api.handleUnblockCard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)

	if response["status"] != "unblocked" {
		t.Errorf("Expected status unblocked, got %v", response["status"])
	}

	if fraudDetector.isBlocked("4532123456789012") {
		t.Error("Expected card to be unblocked in fraud detector")
	}
}

func TestGetFraudStats_Success(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	req := httptest.NewRequest(http.MethodGet, "/api/banking/fraud/stats", nil)
	w := httptest.NewRecorder()

	api.handleFraudStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response fraud.FraudStats
	json.NewDecoder(w.Body).Decode(&response)

	// Просто проверяем, что структура возвращается
	// В моках значения будут нулевыми
}

func TestInvalidJSON(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	req := httptest.NewRequest(http.MethodPost, "/api/banking/transactions", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.handleTransaction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestMissingFields(t *testing.T) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	reqBody := map[string]interface{}{
		"card_number": "4532123456789012",
		// Missing amount and other required fields
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/banking/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.handleTransaction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func BenchmarkProcessTransaction(b *testing.B) {
	fraudDetector := NewMockFraudDetector()
	limitTracker := NewMockLimitTracker()
	api := NewBankingAPI(fraudDetector, limitTracker)

	reqBody := map[string]interface{}{
		"card_number": "4532123456789012",
		"amount":      5000.00,
		"currency":    "RUB",
		"merchant":    "Test Shop",
		"location":    "Moscow",
	}
	body, _ := json.Marshal(reqBody)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/banking/transactions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.handleTransaction(w, req)
	}
}

