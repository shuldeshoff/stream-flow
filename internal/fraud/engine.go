package fraud

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/features"
	"github.com/shuldeshoff/stream-flow/internal/rules"
	"github.com/shuldeshoff/stream-flow/internal/scoring"
)

// Engine is a four-layer antifraud decision system:
//
//	Layer 1 — Pre-checks:   validate required fields, check card blocklist.
//	Layer 2 — Feature build: update online features and take a snapshot.
//	Layer 3 — Rule evaluation: run the configurable rule engine.
//	Layer 4 — Scoring & decision: aggregate risk score, pick final action.
//
// Each layer is fast enough to be on the synchronous (online) path.
// The engine is safe for concurrent use.
type Engine struct {
	blocker  *Blocker
	online   features.OnlineStore
	offline  features.OfflineStore // may be nil
	ruleEng  *rules.Engine
	scorer   *scoring.Scorer
}

// EngineConfig wires all dependencies.
type EngineConfig struct {
	Blocker  *Blocker
	Online   features.OnlineStore
	Offline  features.OfflineStore // optional
	Rules    []rules.Rule          // nil = use DefaultRules
	Scorer   *scoring.Scorer       // nil = use DefaultThresholds
}

// NewEngine creates a ready-to-use Engine.
func NewEngine(cfg EngineConfig) *Engine {
	ruleSet := cfg.Rules
	if len(ruleSet) == 0 {
		ruleSet = rules.DefaultRules()
	}
	scorer := cfg.Scorer
	if scorer == nil {
		scorer = scoring.NewScorer(scoring.DefaultThresholds())
	}
	return &Engine{
		blocker: cfg.Blocker,
		online:  cfg.Online,
		offline: cfg.Offline,
		ruleEng: rules.NewEngine(ruleSet),
		scorer:  scorer,
	}
}

// Evaluate runs all four layers for a transaction and returns a Decision.
// It is safe to call concurrently from multiple goroutines.
func (e *Engine) Evaluate(ctx context.Context, tx *BankTransaction) (scoring.Decision, error) {
	// ── Layer 1: Pre-checks ──────────────────────────────────────────────────
	if err := e.preCheck(tx); err != nil {
		log.Warn().Err(err).Str("tx_id", tx.TransactionID).Msg("Pre-check failed")
		return scoring.Decision{
			TransactionID: tx.TransactionID,
			RiskScore:     1000,
			Action:        rules.ActionBlock,
			ReasonCodes:   []string{"PRE_CHECK_FAILED"},
			ExplainLines:  []string{fmt.Sprintf("Pre-check: %v", err)},
			DecidedAt:     time.Now(),
		}, nil
	}

	// ── Layer 2: Feature snapshot ────────────────────────────────────────────
	tc := toTransactionContext(tx)
	if err := e.online.RecordTransaction(ctx, tc); err != nil {
		log.Warn().Err(err).Str("tx_id", tx.TransactionID).Msg("Feature record failed")
	}

	fv, err := e.online.Snapshot(ctx, tx.CardNumber, tx.UserID, tx.DeviceID)
	if err != nil {
		log.Warn().Err(err).Str("tx_id", tx.TransactionID).Msg("Feature snapshot failed")
		fv = make(features.FeatureVector)
	}

	// Merge offline baselines (best-effort; don't fail on offline store errors).
	if e.offline != nil {
		if baseline, err := e.offline.GetCardBaselines(ctx, tx.CardNumber); err == nil {
			for k, v := range baseline {
				fv[k] = v
			}
		}
		if e.offline != nil && tx.MerchantID != "" {
			if mRisk, err := e.offline.GetMerchantRisk(ctx, tx.MerchantID); err == nil {
				for k, v := range mRisk {
					fv[k] = v
				}
			}
		}
	}

	// Inject per-transaction amount spike ratio if baseline is available.
	if avg30d, ok := fv["card:amount_avg_30d"]; ok && avg30d > 0 {
		fv["card:amount_ratio_vs_avg30d"] = tx.Amount / avg30d
	}

	// ── Layer 3: Rule evaluation ─────────────────────────────────────────────
	triggered := e.ruleEng.Evaluate(fv)

	// ── Layer 4: Scoring & decision ──────────────────────────────────────────
	decision := e.scorer.Score(tx.TransactionID, triggered, fv)

	// Persist block decision to the blocklist.
	if decision.Action == rules.ActionBlock {
		reason := "Engine decision — " + join(decision.ReasonCodes, ", ")
		e.blocker.Block(ctx, tx.CardNumber, reason)
	}

	log.Info().
		Str("tx_id", tx.TransactionID).
		Str("card", tx.CardNumber).
		Int("risk_score", decision.RiskScore).
		Str("action", string(decision.Action)).
		Strs("reasons", decision.ReasonCodes).
		Msg("Fraud evaluation complete")

	return decision, nil
}

// ActiveRuleCount returns the number of enabled rules in the engine.
func (e *Engine) ActiveRuleCount() int {
	return e.ruleEng.RuleCount()
}

// ReloadRules hot-swaps the active rule set without downtime.
func (e *Engine) ReloadRules(newRules []rules.Rule) {
	e.ruleEng.Reload(newRules)
	log.Info().Int("count", e.ruleEng.RuleCount()).Msg("Fraud rules reloaded")
}

// preCheck validates required fields and checks the card blocklist.
func (e *Engine) preCheck(tx *BankTransaction) error {
	if tx.TransactionID == "" {
		return fmt.Errorf("missing transaction_id")
	}
	if tx.CardNumber == "" {
		return fmt.Errorf("missing card_number")
	}
	if tx.Amount <= 0 {
		return fmt.Errorf("invalid amount %.2f", tx.Amount)
	}
	if e.blocker != nil && e.blocker.IsBlocked(tx.CardNumber) {
		return fmt.Errorf("card %s is blocked", tx.CardNumber)
	}
	return nil
}

func toTransactionContext(tx *BankTransaction) features.TransactionContext {
	return features.TransactionContext{
		CardID:     tx.CardNumber,
		AccountID:  tx.AccountID,
		CustomerID: tx.UserID,
		DeviceID:   tx.DeviceID,
		Amount:     tx.Amount,
		Currency:   tx.Currency,
		MerchantID: tx.MerchantID,
		Country:    tx.Location.Country,
		City:       tx.Location.City,
		IPAddress:  tx.IPAddress,
		Timestamp:  tx.Timestamp.UnixMilli(),
	}
}

func join(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
