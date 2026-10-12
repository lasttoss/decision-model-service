// Package decision holds the vocabulary every other package shares: what a provider
// predicts, and what the service answers. It has no logic, which is what keeps `rules`,
// `model` and `decide` from importing each other in a circle.
package decision

import (
	"context"
	"time"

	"github.com/lasttoss/decision-model-service/internal/labelset"
)

// Prediction is one provider's answer about one input.
type Prediction struct {
	Label labelset.Label `json:"label"`
	// Confidence is the provider's own 0..1 estimate. Rules are capped below 1 and never
	// report certainty they do not have; a model reports what it reports, and the
	// service decides whether that is enough to act on.
	Confidence float64 `json:"confidence"`
	// Source names the provider, and it is part of the answer rather than a log line:
	// a caller that cannot tell a rule from a model cannot tell what it is trusting.
	Source string `json:"source"`
	// Detail explains the answer in one line. For rules it names the patterns that
	// matched; for a model it is empty unless explaining was asked for.
	Detail string `json:"detail,omitempty"`
	// LatencyMS, TokensIn and TokensOut belong to the provider's answer, not to the
	// service's handling of it: they are what the provider spent, and they travel with
	// the prediction so the cost table is computed from the numbers a caller is billed.
	LatencyMS float64 `json:"latency_ms"`
	TokensIn  int     `json:"tokens_in,omitempty"`
	TokensOut int     `json:"tokens_out,omitempty"`
}

// Provider answers one input. The interface is this small because the interesting work
// is not in the providers - it is in measuring them.
type Provider interface {
	Name() string
	Predict(ctx context.Context, input string) (Prediction, error)
}

// Outcome is what the service returns: a prediction, how it was reached, and whether the
// service is willing to stand behind the number.
type Outcome struct {
	Prediction
	// Abstained is true when the confidence was below the threshold. The label is still
	// reported - a caller may have a use for the best guess - but it is marked as not
	// acted upon, and the evaluation counts it separately.
	Abstained bool `json:"abstained"`
	// Degraded is true when the preferred provider failed and a fallback answered.
	Degraded bool `json:"degraded"`
	// DegradedReason is filled when Degraded is true, because "it was degraded" without
	// the reason is the kind of message that costs an hour. The values are stable strings
	// (provider_timeout, provider_unparseable, …) because they are used as metric labels.
	DegradedReason string `json:"degraded_reason,omitempty"`
	// TotalMS is what a caller waited: the failed attempt plus the fallback, when there
	// was one. It is the number the evaluation reports as p50/p95, because a latency that
	// excludes the time spent failing is a latency nobody experiences.
	TotalMS float64 `json:"total_ms"`
	// Explain is only ever set when the caller asked for it, and it is always marked as
	// model-generated. It is never mixed into the label.
	Explain        string `json:"explain,omitempty"`
	ExplainByModel bool   `json:"explain_by_model,omitempty"`
}

// Elapsed is a small helper so providers can report their own time. It is here rather
// than in each provider because two different clocks would make the comparison wrong.
func Elapsed(start time.Time) float64 { return float64(time.Since(start).Microseconds()) / 1000 }
