// Package scoring aggregates rule results into a single numeric risk score
// and a structured Decision that the fraud engine returns to callers.
//
// Score scale: 0 (no risk) – 1000 (maximum risk).
// Decision thresholds are configurable and default to:
//
//	0–199   → Allow
//	200–399 → Alert (pass through, log for monitoring)
//	400–599 → Review (soft decline, flag for analyst)
//	600–799 → Challenge (step-up authentication)
//	800–999 → Decline
//	1000    → Block (card-level block)
package scoring

import (
	"sort"
	"time"

	"github.com/shuldeshoff/stream-flow/internal/features"
	"github.com/shuldeshoff/stream-flow/internal/rules"
)

// Decision is the final output of the fraud engine for a single transaction.
type Decision struct {
	// TransactionID links the decision to the originating event.
	TransactionID string `json:"transaction_id"`
	// RiskScore is the aggregate score in [0, 1000].
	RiskScore int `json:"risk_score"`
	// Action is the recommended response.
	Action rules.Action `json:"action"`
	// ReasonCodes is the ordered list of codes from triggered rules.
	ReasonCodes []string `json:"reason_codes"`
	// TriggeredRules contains the IDs of all rules that fired.
	TriggeredRules []string `json:"triggered_rules"`
	// ContributingFeatures lists the feature values relevant to the decision.
	ContributingFeatures features.FeatureVector `json:"contributing_features"`
	// ExplainLines is a human-readable explanation for analysts/audit.
	ExplainLines []string `json:"explain_lines"`
	// DecidedAt is the wall-clock time of the decision.
	DecidedAt time.Time `json:"decided_at"`
}

// Thresholds define the score boundaries for each action.
// All values in [0, 1000]. The highest threshold that is ≤ score determines the action.
type Thresholds struct {
	Alert     int // default 200
	Review    int // default 400
	Challenge int // default 600
	Decline   int // default 800
	Block     int // default 1000
}

// DefaultThresholds returns production-calibrated decision boundaries.
func DefaultThresholds() Thresholds {
	return Thresholds{
		Alert:     200,
		Review:    400,
		Challenge: 600,
		Decline:   800,
		Block:     1000,
	}
}

// Scorer aggregates rule results into a Decision.
type Scorer struct {
	thresholds Thresholds
}

// NewScorer creates a scorer with the given thresholds.
func NewScorer(t Thresholds) *Scorer {
	return &Scorer{thresholds: t}
}

// Score takes the list of triggered rules and the full feature snapshot
// and produces a final Decision.
func (s *Scorer) Score(txID string, triggered []rules.Triggered, fv features.FeatureVector) Decision {
	d := Decision{
		TransactionID:        txID,
		DecidedAt:            time.Now(),
		ContributingFeatures: make(features.FeatureVector),
	}

	if len(triggered) == 0 {
		d.RiskScore = 0
		d.Action = rules.ActionAllow
		return d
	}

	// Sort by descending risk points so highest contributors appear first.
	sort.Slice(triggered, func(i, j int) bool {
		return triggered[i].RiskPoints > triggered[j].RiskPoints
	})

	total := 0
	for _, t := range triggered {
		total += t.RiskPoints
		d.ReasonCodes = append(d.ReasonCodes, t.ReasonCode)
		d.TriggeredRules = append(d.TriggeredRules, t.Rule.ID)
		for k, v := range t.Details {
			d.ContributingFeatures[k] = v
		}
		d.ExplainLines = append(d.ExplainLines, explainLine(t))
	}

	// Cap score at 1000.
	if total > 1000 {
		total = 1000
	}
	d.RiskScore = total
	d.Action = s.actionForScore(total, triggered)

	return d
}

// actionForScore picks the most severe action using both the numeric score
// and the explicit actions from triggered rules.
func (s *Scorer) actionForScore(score int, triggered []rules.Triggered) rules.Action {
	// Take the most severe explicit action from any triggered rule.
	explicit := explicitAction(triggered)

	// Convert threshold to action.
	threshold := s.thresholdAction(score)

	// Return whichever is more severe.
	return moreServerAction(explicit, threshold)
}

func (s *Scorer) thresholdAction(score int) rules.Action {
	switch {
	case score >= s.thresholds.Block:
		return rules.ActionBlock
	case score >= s.thresholds.Decline:
		return rules.ActionDecline
	case score >= s.thresholds.Challenge:
		return rules.ActionChallenge
	case score >= s.thresholds.Review:
		return rules.ActionReview
	case score >= s.thresholds.Alert:
		return rules.ActionAlert
	default:
		return rules.ActionAllow
	}
}

// actionSeverity maps actions to numeric severity for comparison.
var actionSeverity = map[rules.Action]int{
	rules.ActionAllow:     0,
	rules.ActionAlert:     1,
	rules.ActionReview:    2,
	rules.ActionChallenge: 3,
	rules.ActionDecline:   4,
	rules.ActionBlock:     5,
}

func moreServerAction(a, b rules.Action) rules.Action {
	if actionSeverity[a] >= actionSeverity[b] {
		return a
	}
	return b
}

func explicitAction(triggered []rules.Triggered) rules.Action {
	best := rules.ActionAllow
	for _, t := range triggered {
		if actionSeverity[t.Action] > actionSeverity[best] {
			best = t.Action
		}
	}
	return best
}

func explainLine(t rules.Triggered) string {
	line := "[" + t.Rule.ID + "] " + t.Rule.Name + " → " + string(t.Action) +
		" (+" + itoa(t.RiskPoints) + " pts)"
	return line
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
