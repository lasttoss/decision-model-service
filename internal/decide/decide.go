// Package decide is the service's whole behaviour: try providers in order, fall back with
// a reason rather than an apology, abstain when the confidence is low, and never let an
// optional explanation fail a decision.
package decide

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
	"github.com/lasttoss/decision-model-service/internal/metrics"
	"github.com/lasttoss/decision-model-service/internal/model"
)

// Degradation reasons are stable strings rather than formatted sentences, because they
// end up as metric label values and in the evaluation report: "provider_timeout" can be
// counted and graphed, "the model did not answer in time" cannot.
const (
	ReasonTimeout     = "provider_timeout"
	ReasonUnavailable = "provider_unavailable"
	ReasonUnparseable = "provider_unparseable"
	ReasonClientError = "provider_client_error"
	ReasonError       = "provider_error"
	ReasonNoProvider  = "no_provider_answered"
)

// Explainer is the optional half of a provider. A provider that cannot explain is still a
// provider; a service that required one would be unable to use the rule baseline.
type Explainer interface {
	Explain(ctx context.Context, input string, label labelset.Label) (string, error)
}

type Service struct {
	// Providers are tried in order. The first that answers wins, and any fallback is
	// reported on the answer itself rather than only in a log line.
	Providers []decision.Provider
	// Threshold is the confidence below which the service abstains. It is a required
	// field rather than a default, because the number is a policy decision: 0.6 in a tool
	// that pages a human, 0.9 in one that retries automatically.
	Threshold float64
	// Explain asks providers for one sentence. Off unless a caller asks, so the default
	// path costs one call and no output tokens.
	Explain  bool
	Counters *metrics.Counters
}

func (s *Service) counters() *metrics.Counters {
	if s.Counters == nil {
		s.Counters = metrics.New()
	}
	return s.Counters
}

func (s *Service) Names() []string {
	out := make([]string, 0, len(s.Providers))
	for _, p := range s.Providers {
		out = append(out, p.Name())
	}
	return out
}

// Decide always returns an outcome rather than an error, and the outcome always says how
// it was reached. A caller in a log pipeline has to do something with the line, and
// "nothing" is not one of the options - so the service answers needs_human with zero
// confidence and degraded=true when every provider is down, which is a decision a person
// can act on. The trade is written down in docs/design-notes.md.
func (s *Service) Decide(ctx context.Context, input string) decision.Outcome {
	start := time.Now()
	var lastErr error

	for i, provider := range s.Providers {
		pred, err := provider.Predict(ctx, input)
		if err != nil {
			lastErr = err
			s.counters().AddDegraded(reasonOf(err))
			continue
		}

		out := decision.Outcome{Prediction: pred}
		if i > 0 {
			// A fallback answered. Saying so is the difference between a degradation and
			// a silent change of behaviour.
			out.Degraded = true
			out.DegradedReason = reasonOf(lastErr)
		}
		out.Abstained = pred.Confidence < s.Threshold
		out.TotalMS = float64(time.Since(start).Microseconds()) / 1000

		if s.Explain && !out.Abstained {
			// An explanation is a nice-to-have: it is attempted, counted, and dropped if
			// it fails. There is a test for exactly this.
			if explainer, ok := provider.(Explainer); ok {
				sentence, err := explainer.Explain(ctx, input, pred.Label)
				switch {
				case err != nil:
					s.counters().AddExplainFailure()
				case strings.TrimSpace(sentence) != "":
					out.Explain = strings.TrimSpace(sentence)
					out.ExplainByModel = true
				}
			}
		}

		s.counters().AddDecision(out.Abstained, out.Explain != "")
		return out
	}

	s.counters().AddDegraded(ReasonNoProvider)
	s.counters().AddDecision(true, false)
	return decision.Outcome{
		Prediction: decision.Prediction{
			Label: labelset.NeedsHuman, Confidence: 0, Source: "none",
			Detail: "no provider answered",
		},
		Abstained:      true,
		Degraded:       true,
		DegradedReason: pick(lastErr, ReasonNoProvider),
		TotalMS:        float64(time.Since(start).Microseconds()) / 1000,
	}
}

func pick(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return reasonOf(err)
}

func reasonOf(err error) string {
	switch {
	case err == nil:
		return ReasonError
	case errors.Is(err, model.ErrUnparseable):
		return ReasonUnparseable
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout
	case errors.Is(err, context.Canceled):
		return ReasonTimeout
	}
	var httpErr *model.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Status >= 500 {
			return ReasonUnavailable
		}
		return ReasonClientError
	}
	// http.Client reports a timeout as an error string rather than a sentinel, which is
	// why this last check exists instead of a tidy errors.Is.
	if msg := strings.ToLower(err.Error()); strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
		return ReasonTimeout
	}
	return ReasonError
}
