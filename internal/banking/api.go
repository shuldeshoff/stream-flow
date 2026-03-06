package banking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/cache"
	"github.com/shuldeshoff/stream-flow/internal/fraud"
	"github.com/shuldeshoff/stream-flow/internal/models"
	"github.com/shuldeshoff/stream-flow/internal/processor"
	"github.com/shuldeshoff/stream-flow/internal/rules"
)

// BankingAPI handles banking transactions through the full 4-layer fraud pipeline:
// limit check → pre-checks → feature snapshot → rule evaluation → scoring → decision.
type BankingAPI struct {
	processor    *processor.EventProcessor
	fraudEngine  *fraud.Engine
	limitTracker *fraud.LimitTracker
	blocker      *fraud.Blocker
	cache        *cache.RedisCache
	server       *http.Server
	port         int
}

// NewBankingAPI creates a Banking API. fraudEngine may be nil — in that case
// transactions are approved after limit checks only (useful in tests / no-Redis env).
func NewBankingAPI(port int, proc *processor.EventProcessor, redisCache *cache.RedisCache, fraudEngine *fraud.Engine) *BankingAPI {
	return &BankingAPI{
		processor:    proc,
		fraudEngine:  fraudEngine,
		limitTracker: fraud.NewLimitTracker(redisCache),
		blocker:      fraud.NewBlocker(redisCache, 24*time.Hour),
		cache:        redisCache,
		port:         port,
	}
}

func (api *BankingAPI) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/banking/transaction", api.handleTransaction)
	mux.HandleFunc("/api/v1/banking/limits", api.handleGetLimits)
	mux.HandleFunc("/api/v1/banking/fraud/stats", api.handleFraudStats)
	mux.HandleFunc("/api/v1/banking/card/block", api.handleBlockCard)
	mux.HandleFunc("/api/v1/banking/card/unblock", api.handleUnblockCard)

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

// handleTransaction runs the full pipeline for a single transaction request.
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
	if tx.Timestamp.IsZero() {
		tx.Timestamp = time.Now()
	}

	// ── Layer 0: limit check (before touching the fraud engine) ───────────────
	limitResult := api.limitTracker.CheckLimits(r.Context(), &tx)
	if !limitResult.Allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "declined",
			"reason": limitResult.Reason,
			"limit":  limitResult.Limit,
			"spent":  limitResult.CurrentSpent,
		})
		return
	}

	// ── Layers 1-4: fraud engine (pre-checks, features, rules, scoring) ───────
	if api.fraudEngine != nil {
		decision, err := api.fraudEngine.Evaluate(r.Context(), &tx)
		if err != nil {
			log.Error().Err(err).Str("tx_id", tx.TransactionID).Msg("Fraud engine error")
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		if decision.Action == rules.ActionDecline || decision.Action == rules.ActionBlock {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":          "declined_by_fraud",
				"action":          decision.Action,
				"risk_score":      decision.RiskScore,
				"reason_codes":    decision.ReasonCodes,
				"triggered_rules": decision.TriggeredRules,
				"explain":         decision.ExplainLines,
			})
			api.logFraudDecision(&tx, decision.RiskScore, decision.ReasonCodes)
			return
		}

		if decision.Action == rules.ActionChallenge || decision.Action == rules.ActionReview {
			// Pass through but annotate the response so the caller can act.
			w.Header().Set("X-Fraud-Risk-Score", fmt.Sprintf("%d", decision.RiskScore))
			w.Header().Set("X-Fraud-Action", string(decision.Action))
		}
	}

	// ── Approved: record spend and emit analytics event ───────────────────────
	api.limitTracker.RecordTransaction(r.Context(), &tx)

	event := api.transactionToEvent(&tx)
	if err := api.processor.Submit(event); err != nil {
		log.Error().Err(err).Msg("Failed to submit transaction event")
	}

	w.Header().Set("Content-Type", "application/json")
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(api.limitTracker.GetLimitsStatus(r.Context(), cardNumber))
}

func (api *BankingAPI) handleFraudStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// The new engine exposes stats through Prometheus metrics;
	// return a minimal payload for backward compatibility.
	json.NewEncoder(w).Encode(map[string]interface{}{
		"engine": "fraud.Engine v2 (feature-store backed)",
		"note":   "per-rule metrics available at /metrics (Prometheus)",
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
	if req.CardNumber == "" {
		http.Error(w, "card_number required", http.StatusBadRequest)
		return
	}

	api.blocker.Block(r.Context(), req.CardNumber, req.Reason)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "blocked",
		"card":   req.CardNumber,
	})
}

func (api *BankingAPI) handleUnblockCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CardNumber string `json:"card_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	api.blocker.Unblock(r.Context(), req.CardNumber)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "unblocked",
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

func (api *BankingAPI) logFraudDecision(tx *fraud.BankTransaction, riskScore int, reasonCodes []string) {
	event := models.Event{
		ID:        fmt.Sprintf("fraud_%s", tx.TransactionID),
		Type:      "fraud_detected",
		Source:    "fraud_engine_v2",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"transaction_id": tx.TransactionID,
			"card_number":    tx.CardNumber,
			"amount":         tx.Amount,
			"merchant_id":    tx.MerchantID,
			"risk_score":     riskScore,
			"reason_codes":   reasonCodes,
		},
	}
	api.processor.Submit(event)
}
