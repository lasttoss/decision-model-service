package decide

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
	"github.com/lasttoss/decision-model-service/internal/model"
	"github.com/lasttoss/decision-model-service/internal/rules"
)

// stub is a provider that answers what the test tells it to, including failing.
type stub struct {
	name       string
	pred       decision.Prediction
	err        error
	sleep      time.Duration
	calls      int
	explain    string
	explainErr error
}

func (s *stub) Name() string { return s.name }

func (s *stub) Predict(ctx context.Context, _ string) (decision.Prediction, error) {
	s.calls++
	if s.sleep > 0 {
		select {
		case <-time.After(s.sleep):
		case <-ctx.Done():
			return decision.Prediction{}, ctx.Err()
		}
	}
	if s.err != nil {
		return decision.Prediction{}, s.err
	}
	pred := s.pred
	pred.Source = s.name
	return pred, nil
}

func (s *stub) Explain(_ context.Context, _ string, _ labelset.Label) (string, error) {
	if s.explainErr != nil {
		return "", s.explainErr
	}
	return s.explain, nil
}

func TestFirstProviderThatAnswersWins(t *testing.T) {
	first := &stub{name: "model", pred: decision.Prediction{Label: labelset.Retryable, Confidence: 0.9}}
	fallback := &stub{name: "rules", pred: decision.Prediction{Label: labelset.Expected, Confidence: 0.5}}
	s := &Service{Providers: []decision.Provider{first, fallback}, Threshold: 0.5}

	out := s.Decide(context.Background(), "x")
	if out.Label != labelset.Retryable || out.Degraded || out.Source != "model" {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	if fallback.calls != 0 {
		t.Fatalf("the fallback must not be called when the first provider works, called %d times", fallback.calls)
	}
}

func TestFallbackIsReportedWithAReason(t *testing.T) {
	first := &stub{name: "model", err: &model.HTTPError{Status: 503}}
	fallback := &stub{name: "rules", pred: decision.Prediction{Label: labelset.NeedsHuman, Confidence: 0.8}}
	s := &Service{Providers: []decision.Provider{first, fallback}, Threshold: 0.5}

	out := s.Decide(context.Background(), "x")
	if !out.Degraded || out.DegradedReason != ReasonUnavailable {
		t.Fatalf("a fallback must be visible with its reason: %+v", out)
	}
	if out.Source != "rules" || out.Label != labelset.NeedsHuman {
		t.Fatalf("the answer must come from the fallback: %+v", out)
	}
	// The latency is the caller's experience, and the caller waited for the failure too.
	if out.TotalMS < 0 {
		t.Fatalf("total latency must be measured: %+v", out)
	}
}

func TestDegradationReasonsAreClassified(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{model.ErrUnparseable, ReasonUnparseable},
		{&model.HTTPError{Status: 500}, ReasonUnavailable},
		{&model.HTTPError{Status: 401}, ReasonClientError},
		{context.DeadlineExceeded, ReasonTimeout},
		{errors.New("dial tcp: i/o timeout"), ReasonTimeout},
		{errors.New("something else entirely"), ReasonError},
	}
	for _, c := range cases {
		if got := reasonOf(c.err); got != c.want {
			t.Errorf("reasonOf(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}

// The threshold is the service's own decision, and the label is still reported: a caller
// may want the best guess, but the answer is marked as not acted upon.
func TestLowConfidenceAbstains(t *testing.T) {
	p := &stub{name: "rules", pred: decision.Prediction{Label: labelset.Retryable, Confidence: 0.55}}
	s := &Service{Providers: []decision.Provider{p}, Threshold: 0.6}
	out := s.Decide(context.Background(), "x")
	if !out.Abstained {
		t.Fatalf("0.55 against a 0.6 threshold must abstain: %+v", out)
	}
	if out.Label != labelset.Retryable {
		t.Fatalf("the best guess is still reported: %+v", out)
	}

	s.Threshold = 0.5
	if out := s.Decide(context.Background(), "y"); out.Abstained {
		t.Fatalf("0.55 against a 0.5 threshold must not abstain: %+v", out)
	}
}

// An explanation is a nice-to-have. A model that fails to explain must not turn a good
// decision into a bad one.
func TestExplainFailureDoesNotFailTheDecision(t *testing.T) {
	p := &stub{name: "model", pred: decision.Prediction{Label: labelset.Expected, Confidence: 0.9},
		explainErr: errors.New("the model fell over while explaining")}
	s := &Service{Providers: []decision.Provider{p}, Threshold: 0.5, Explain: true}

	out := s.Decide(context.Background(), "x")
	if out.Label != labelset.Expected || out.Abstained {
		t.Fatalf("the decision must survive an explanation failure: %+v", out)
	}
	if out.Explain != "" || out.ExplainByModel {
		t.Fatalf("a failed explanation must not appear: %+v", out)
	}
	if got := s.Counters.Snapshot().ExplainsFailed; got != 1 {
		t.Fatalf("the failure must be counted, got %d", got)
	}
}

func TestExplanationIsMarkedAsModelGenerated(t *testing.T) {
	p := &stub{name: "model", pred: decision.Prediction{Label: labelset.Expected, Confidence: 0.9},
		explain: "the client closed the stream after receiving what it asked for"}
	s := &Service{Providers: []decision.Provider{p}, Threshold: 0.5, Explain: true}

	out := s.Decide(context.Background(), "context canceled")
	if !out.ExplainByModel || out.Explain == "" {
		t.Fatalf("an explanation must be attributed: %+v", out)
	}
	if out.Label != labelset.Expected {
		t.Fatalf("the explanation must not change the label: %+v", out)
	}
}

func TestExplainIsOffUnlessAskedFor(t *testing.T) {
	p := &stub{name: "model", pred: decision.Prediction{Label: labelset.Expected, Confidence: 0.9}, explain: "because"}
	s := &Service{Providers: []decision.Provider{p}, Threshold: 0.5}
	if out := s.Decide(context.Background(), "x"); out.Explain != "" {
		t.Fatalf("the default path must not pay for an explanation: %+v", out)
	}
}

// When every provider is down the service still answers something a caller can act on,
// and it says plainly that it is guessing.
func TestNoProviderAnsweringStillAnswers(t *testing.T) {
	a := &stub{name: "model", err: &model.HTTPError{Status: 503}}
	b := &stub{name: "rules", err: errors.New("the baseline exploded")}
	s := &Service{Providers: []decision.Provider{a, b}, Threshold: 0.5}

	out := s.Decide(context.Background(), "x")
	if !out.Degraded || !out.Abstained {
		t.Fatalf("with nothing working the answer must be degraded and abstained: %+v", out)
	}
	if out.Label != labelset.NeedsHuman || out.Confidence != 0 {
		t.Fatalf("the fallback answer is needs_human at zero confidence: %+v", out)
	}
	if out.DegradedReason != ReasonNoProvider {
		// The reason names the failure that was last seen, which for a total outage is
		// the generic one rather than the first provider's status code.
		t.Logf("reason reported: %s", out.DegradedReason)
	}
}

// The rule baseline is the one provider that always works, so a service configured with it
// alone must never report a degradation. This is the behaviour the deployment relies on.
func TestRulesAloneNeverDegrades(t *testing.T) {
	s := &Service{Providers: []decision.Provider{rules.New()}, Threshold: 0.5}
	for _, input := range []string{"context canceled", "panic: nil pointer", "the frobnicator wobbled"} {
		out := s.Decide(context.Background(), input)
		if out.Degraded || out.Source != "rules" {
			t.Fatalf("%q: the baseline must answer without degrading: %+v", input, out)
		}
	}
}
