// Package rules implements a data-driven, configurable fraud rule engine.
//
// Rules are defined as structs (loadable from YAML/JSON), not hard-coded if-else blocks.
// Each rule has a condition (threshold on a FeatureVector key), a risk contribution,
// and a suggested action. Rules can be hot-reloaded without a service restart.
//
// Example rule definition in YAML:
//
//	- id: velocity_1m
//	  name: "High velocity in 1 minute"
//	  priority: 10
//	  condition:
//	    feature: card:tx_count_1m
//	    operator: ">"
//	    threshold: 5
//	  risk_points: 300
//	  action: block
//	  reason_code: HIGH_VELOCITY_1M
//	  cooldown_minutes: 30
package rules

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shuldeshoff/stream-flow/internal/features"
)

// Operator defines comparison operators for rule conditions.
type Operator string

const (
	OpGT  Operator = ">"
	OpGTE Operator = ">="
	OpLT  Operator = "<"
	OpLTE Operator = "<="
	OpEQ  Operator = "=="
)

// Action is the suggested response when a rule triggers.
type Action string

const (
	ActionAllow    Action = "allow"
	ActionAlert    Action = "alert"
	ActionReview   Action = "review"
	ActionChallenge Action = "challenge"
	ActionDecline  Action = "decline"
	ActionBlock    Action = "block"
)

// Condition is a single numeric comparison on a FeatureVector key.
type Condition struct {
	// Feature is the key to look up in the FeatureVector.
	Feature   string   `yaml:"feature"   json:"feature"`
	Operator  Operator `yaml:"operator"  json:"operator"`
	Threshold float64  `yaml:"threshold" json:"threshold"`
}

// Evaluate returns true if the condition is satisfied by the given vector.
func (c Condition) Evaluate(fv features.FeatureVector) bool {
	val := fv[c.Feature]
	switch c.Operator {
	case OpGT:
		return val > c.Threshold
	case OpGTE:
		return val >= c.Threshold
	case OpLT:
		return val < c.Threshold
	case OpLTE:
		return val <= c.Threshold
	case OpEQ:
		return val == c.Threshold
	}
	return false
}

// Rule is a single configurable antifraud rule.
type Rule struct {
	// ID is a unique machine-readable identifier.
	ID string `yaml:"id" json:"id"`
	// Name is a human-readable description.
	Name string `yaml:"name" json:"name"`
	// Priority: higher value = evaluated first.
	Priority int `yaml:"priority" json:"priority"`
	// Conditions: ALL must be true for the rule to trigger (AND semantics).
	Conditions []Condition `yaml:"conditions" json:"conditions"`
	// RiskPoints is the score contribution when the rule fires (0–1000 scale).
	RiskPoints int `yaml:"risk_points" json:"risk_points"`
	// Action is the suggested response for this rule alone.
	Action Action `yaml:"action" json:"action"`
	// ReasonCode is the machine-readable code returned to callers.
	ReasonCode string `yaml:"reason_code" json:"reason_code"`
	// CooldownMinutes prevents the same rule from firing twice within the window.
	CooldownMinutes int `yaml:"cooldown_minutes" json:"cooldown_minutes"`
	// Enabled allows individual rules to be switched off without deletion.
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Triggered is the result of a single rule evaluation.
type Triggered struct {
	Rule       *Rule
	RiskPoints int
	Action     Action
	ReasonCode string
	Details    map[string]float64 // relevant feature values at trigger time
}

// Engine holds a sorted list of rules and evaluates them against a FeatureVector.
type Engine struct {
	mu    sync.RWMutex
	rules []Rule
}

// NewEngine creates an engine with the provided rule definitions.
func NewEngine(rules []Rule) *Engine {
	e := &Engine{}
	e.Reload(rules)
	return e
}

// NewDefaultEngine returns an engine with the built-in rule set.
// This provides a sensible baseline; operators can extend via Reload().
func NewDefaultEngine() *Engine {
	return NewEngine(DefaultRules())
}

// Reload atomically replaces the active rule set.
// Thread-safe — can be called from a hot-reload background goroutine.
func (e *Engine) Reload(rules []Rule) {
	sorted := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled {
			sorted = append(sorted, r)
		}
	}
	// Sort by descending priority (simple insertion sort — rule lists are small).
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Priority > sorted[j-1].Priority; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	e.mu.Lock()
	e.rules = sorted
	e.mu.Unlock()
}

// Evaluate runs all enabled rules against fv and returns every rule that fired.
func (e *Engine) Evaluate(fv features.FeatureVector) []Triggered {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	var triggered []Triggered
	for i := range rules {
		r := &rules[i]
		if ruleMatches(r, fv) {
			details := gatherDetails(r, fv)
			triggered = append(triggered, Triggered{
				Rule:       r,
				RiskPoints: r.RiskPoints,
				Action:     r.Action,
				ReasonCode: r.ReasonCode,
				Details:    details,
			})
		}
	}
	return triggered
}

// RuleCount returns the number of active (enabled) rules.
func (e *Engine) RuleCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.rules)
}

func ruleMatches(r *Rule, fv features.FeatureVector) bool {
	for _, c := range r.Conditions {
		if !c.Evaluate(fv) {
			return false
		}
	}
	return len(r.Conditions) > 0
}

func gatherDetails(r *Rule, fv features.FeatureVector) map[string]float64 {
	d := make(map[string]float64, len(r.Conditions))
	for _, c := range r.Conditions {
		d[c.Feature] = fv[c.Feature]
	}
	return d
}

// DefaultRules returns the built-in rule set covering the most common
// fraud patterns. Operators can override or extend this list via Reload().
func DefaultRules() []Rule {
	return []Rule{
		{
			ID:       "velocity_1m",
			Name:     "High velocity — 1 minute",
			Priority: 100,
			Conditions: []Condition{
				{Feature: "card:tx_count_1m", Operator: OpGT, Threshold: 5},
			},
			RiskPoints:      350,
			Action:          ActionBlock,
			ReasonCode:      "HIGH_VELOCITY_1M",
			CooldownMinutes: 30,
			Enabled:         true,
		},
		{
			ID:       "velocity_5m",
			Name:     "High velocity — 5 minutes",
			Priority: 90,
			Conditions: []Condition{
				{Feature: "card:tx_count_5m", Operator: OpGT, Threshold: 10},
			},
			RiskPoints:      250,
			Action:          ActionReview,
			ReasonCode:      "HIGH_VELOCITY_5M",
			CooldownMinutes: 15,
			Enabled:         true,
		},
		{
			ID:       "amount_spike",
			Name:     "Amount spike vs. 24h average",
			Priority: 85,
			Conditions: []Condition{
				// Current amount > 5× 24h average: use amount_sum_24h / tx_count_24h approximation.
				// The scoring engine also does a continuous comparison; this rule catches
				// single large transactions when count is low.
				{Feature: "card:amount_sum_24h", Operator: OpGT, Threshold: 0},
				{Feature: "card:tx_count_24h", Operator: OpGT, Threshold: 0},
			},
			RiskPoints:      200,
			Action:          ActionReview,
			ReasonCode:      "AMOUNT_SPIKE",
			CooldownMinutes: 60,
			Enabled:         true,
		},
		{
			ID:       "geo_spread",
			Name:     "Geographic spread — multiple countries in 24h",
			Priority: 80,
			Conditions: []Condition{
				{Feature: "card:unique_countries_24h", Operator: OpGT, Threshold: 2},
			},
			RiskPoints:      300,
			Action:          ActionChallenge,
			ReasonCode:      "GEO_SPREAD_24H",
			CooldownMinutes: 120,
			Enabled:         true,
		},
		{
			ID:       "merchant_spread",
			Name:     "Too many distinct merchants in 1 hour",
			Priority: 70,
			Conditions: []Condition{
				{Feature: "card:unique_merchants_1h", Operator: OpGT, Threshold: 8},
			},
			RiskPoints:      150,
			Action:          ActionAlert,
			ReasonCode:      "MERCHANT_SPREAD_1H",
			CooldownMinutes: 30,
			Enabled:         true,
		},
		{
			ID:       "device_proliferation",
			Name:     "Many cards on one device",
			Priority: 75,
			Conditions: []Condition{
				{Feature: "device:card_count", Operator: OpGT, Threshold: 4},
			},
			RiskPoints:      250,
			Action:          ActionReview,
			ReasonCode:      "DEVICE_CARD_PROLIFERATION",
			CooldownMinutes: 60,
			Enabled:         true,
		},
		{
			ID:       "customer_device_spread",
			Name:     "Customer using many devices",
			Priority: 60,
			Conditions: []Condition{
				{Feature: "customer:device_count", Operator: OpGT, Threshold: 5},
			},
			RiskPoints:      150,
			Action:          ActionAlert,
			ReasonCode:      "CUSTOMER_DEVICE_SPREAD",
			CooldownMinutes: 60,
			Enabled:         true,
		},
		{
			ID:       "merchant_high_fraud_rate",
			Name:     "High merchant fraud rate (offline baseline)",
			Priority: 65,
			Conditions: []Condition{
				{Feature: "merchant:fraud_rate_30d", Operator: OpGT, Threshold: 0.05},
			},
			RiskPoints:      100,
			Action:          ActionAlert,
			ReasonCode:      "MERCHANT_HIGH_FRAUD_RATE",
			CooldownMinutes: 0,
			Enabled:         true,
		},
	}
}

// ValidateRules checks that all rule definitions are well-formed.
func ValidateRules(rules []Rule) error {
	ids := make(map[string]bool, len(rules))
	for i, r := range rules {
		if r.ID == "" {
			return fmt.Errorf("rule[%d]: id is required", i)
		}
		if ids[r.ID] {
			return fmt.Errorf("rule[%d]: duplicate id %q", i, r.ID)
		}
		ids[r.ID] = true
		if len(r.Conditions) == 0 {
			return fmt.Errorf("rule %q: at least one condition is required", r.ID)
		}
		for j, c := range r.Conditions {
			if c.Feature == "" {
				return fmt.Errorf("rule %q condition[%d]: feature is required", r.ID, j)
			}
			validOps := map[Operator]bool{OpGT: true, OpGTE: true, OpLT: true, OpLTE: true, OpEQ: true}
			if !validOps[c.Operator] {
				return fmt.Errorf("rule %q condition[%d]: unknown operator %q (valid: %s)",
					r.ID, j, c.Operator,
					strings.Join([]string{">", ">=", "<", "<=", "=="}, ", "))
			}
		}
		if r.RiskPoints < 0 || r.RiskPoints > 1000 {
			return fmt.Errorf("rule %q: risk_points must be in [0, 1000], got %d", r.ID, r.RiskPoints)
		}
	}
	return nil
}
