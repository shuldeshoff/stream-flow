package banking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/cache"
	"github.com/sul/streamflow/internal/fraud"
	"github.com/sul/streamflow/internal/models"
	"github.com/sul/streamflow/internal/processor"
)

// BankingAPI обрабатывает банковские транзакции
type BankingAPI struct {
	processor      *processor.EventProcessor
	fraudDetector  *fraud.FraudDetector
	limitTracker   *fraud.LimitTracker
	cache          *cache.RedisCache
	server         *http.Server
	port           int
}

func NewBankingAPI(port int, proc *processor.EventProcessor, cache *cache.RedisCache) *BankingAPI {
	return &BankingAPI{
		processor:     proc,
		fraudDetector: fraud.NewFraudDetector(cache),
		limitTracker:  fraud.NewLimitTracker(cache),
		cache:         cache,
		port:          port,
	}
}

func (api *BankingAPI) Start() error {
	mux := http.NewServeMux()

	// Banking endpoints
	mux.HandleFunc("/api/v1/banking/transaction", api.handleTransaction)
	mux.HandleFunc("/api/v1/banking/limits", api.handleGetLimits)
	mux.HandleFunc("/api/v1/banking/fraud/stats", api.handleFraudStats)
	mux.HandleFunc("/api/v1/banking/card/block", api.handleBlockCard)

	api.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", api.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Info().Int("port", api.port).Msg("Banking API server started")
	return api.server.ListenAndServe()
}

func (api *BankingAPI) Shutdown(ctx context.Context) error {
	return api.server.Shutdown(ctx)
}

// handleTransaction обрабатывает банковскую транзакцию
func (api *BankingAPI) handleTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var tx fraud.BankTransaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// 1. Проверяем лимиты
	limitResult := api.limitTracker.CheckLimits(r.Context(), &tx)
	if !limitResult.Allowed {
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "declined",
			"reason": limitResult.Reason,
			"limit":  limitResult.Limit,
			"spent":  limitResult.CurrentSpent,
		})
		return
	}

	// 2. Проверяем на fraud
	fraudResult := api.fraudDetector.CheckTransaction(r.Context(), &tx)
	if fraudResult.IsFraud {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "fraud_detected",
			"reason":         fraudResult.Reason,
			"confidence":     fraudResult.Confidence,
			"action":         fraudResult.Action,
			"triggered_rule": fraudResult.TriggeredRule,
			"details":        fraudResult.Details,
		})

		// Логируем fraud событие
		api.logFraudEvent(&tx, fraudResult)
		return
	}

	// 3. Записываем транзакцию для лимитов
	api.limitTracker.RecordTransaction(r.Context(), &tx)

	// 4. Отправляем в StreamFlow для аналитики
	event := api.transactionToEvent(&tx)
	if err := api.processor.Submit(event); err != nil {
		log.Error().Err(err).Msg("Failed to submit transaction event")
	}

	// 5. Возвращаем успех
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "approved",
		"transaction_id":  tx.TransactionID,
		"amount":          tx.Amount,
		"remaining_limit": limitResult.RemainingLimit,
	})

	log.Info().
		Str("transaction_id", tx.TransactionID).
		Str("card", tx.CardNumber).
		Float64("amount", tx.Amount).
		Msg("Transaction approved")
}

func (api *BankingAPI) handleGetLimits(w http.ResponseWriter, r *http.Request) {
	cardNumber := r.URL.Query().Get("card")
	if cardNumber == "" {
		http.Error(w, "Card number required", http.StatusBadRequest)
		return
	}

	status := api.limitTracker.GetLimitsStatus(r.Context(), cardNumber)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (api *BankingAPI) handleFraudStats(w http.ResponseWriter, r *http.Request) {
	stats := api.fraudDetector.GetStats()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_checked":   stats.TotalChecked,
		"fraud_detected":  stats.FraudDetected,
		"cards_blocked":   stats.CardsBlocked,
		"fraud_rate":      float64(stats.FraudDetected) / float64(stats.TotalChecked) * 100,
	})
}

func (api *BankingAPI) handleBlockCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CardNumber string `json:"card_number"`
		Reason     string `json:"reason"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	api.fraudDetector.BlockCard(req.CardNumber, req.Reason)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "blocked",
		"card":   req.CardNumber,
	})
}

func (api *BankingAPI) transactionToEvent(tx *fraud.BankTransaction) models.Event {
	return models.Event{
		ID:        tx.TransactionID,
		Type:      "bank_transaction",
		Source:    "banking_api",
		Timestamp: tx.Timestamp,
		Data: map[string]interface{}{
			"card_number":   tx.CardNumber,
			"amount":        tx.Amount,
			"currency":      tx.Currency,
			"merchant_id":   tx.MerchantID,
			"merchant_name": tx.MerchantName,
			"merchant_mcc":  tx.MerchantMCC,
			"ip":            tx.IPAddress,
			"device_id":     tx.DeviceID,
			"card_type":     tx.CardType,
			"account_id":    tx.AccountID,
			"user_id":       tx.UserID,
		},
		Metadata: map[string]string{
			"country": tx.Location.Country,
			"city":    tx.Location.City,
		},
	}
}

func (api *BankingAPI) logFraudEvent(tx *fraud.BankTransaction, result *fraud.FraudResult) {
	event := models.Event{
		ID:        fmt.Sprintf("fraud_%s", tx.TransactionID),
		Type:      "fraud_detected",
		Source:    "fraud_detector",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"transaction_id":  tx.TransactionID,
			"card_number":     tx.CardNumber,
			"amount":          tx.Amount,
			"merchant_id":     tx.MerchantID,
			"confidence":      result.Confidence,
			"reason":          result.Reason,
			"triggered_rule":  result.TriggeredRule,
			"action":          result.Action,
		},
	}

	api.processor.Submit(event)
}

