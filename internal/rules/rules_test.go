package rules

import (
	"context"
	"testing"

	"github.com/lasttoss/decision-model-service/internal/labelset"
)

func decide(t *testing.T, input string) (labelset.Label, float64) {
	t.Helper()
	pred, err := New().Predict(context.Background(), input)
	if err != nil {
		t.Fatalf("predict %q: %v", input, err)
	}
	return pred.Label, pred.Confidence
}

func TestObviousCases(t *testing.T) {
	cases := []struct {
		input string
		want  labelset.Label
	}{
		{"rpc error: code = DeadlineExceeded desc = context deadline exceeded", labelset.Retryable},
		{"connection reset by peer", labelset.Retryable},
		{"upstream returned 503 Service Unavailable", labelset.Retryable},
		{"context canceled", labelset.Expected},
		{"readiness probe failed: connection refused", labelset.Retryable}, // the probe is not the signal; the refusal is
		{"panic: runtime error: nil pointer dereference", labelset.NeedsHuman},
		{"write /var/lib/data: no space left on device", labelset.NeedsHuman},
		{"checksum mismatch on segment 41", labelset.NeedsHuman},
		{"client disconnected while streaming", labelset.Expected},
	}
	for _, c := range cases {
		if got, _ := decide(t, c.input); got != c.want {
			t.Errorf("%q: want %s, got %s", c.input, c.want, got)
		}
	}
}

// The case this package is meant to lose: two signals in one line, one far more specific
// than the other. A keyword baseline gets it right here only because the weights were
// chosen deliberately, and the evaluation contains the harder versions of the same shape.
func TestSpecificSignalOutweighsTheGeneralOne(t *testing.T) {
	got, _ := decide(t, "timeout talking to the secrets backend: permission denied")
	if got != labelset.NeedsHuman {
		t.Fatalf("permission denied must outweigh timeout, got %s", got)
	}
}

func TestNothingMatchedIsLowConfidence(t *testing.T) {
	label, conf := decide(t, "the frobnicator wobbled")
	if label != labelset.NeedsHuman {
		t.Fatalf("an unrecognised line should be needs_human, got %s", label)
	}
	if conf > 0.35 {
		t.Fatalf("an unmatched line must not be confident, got %.2f", conf)
	}
}

func TestIsDeterministic(t *testing.T) {
	p := New()
	first, err := p.Predict(context.Background(), "timeout and permission denied")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := p.Predict(context.Background(), "timeout and permission denied")
		if err != nil {
			t.Fatal(err)
		}
		if again.Label != first.Label || again.Confidence != first.Confidence || again.Detail != first.Detail {
			t.Fatalf("rule answers must be identical every time: %+v vs %+v", first, again)
		}
	}
}

func TestConfidenceIsCappedBelowCertainty(t *testing.T) {
	_, conf := decide(t, "panic: nil pointer dereference: segmentation fault: invariant violated: checksum mismatch: permission denied")
	if conf > MaxConfidence {
		t.Fatalf("a pattern table must never claim certainty, got %.2f", conf)
	}
	if conf < 0.5 {
		t.Fatalf("a strong agreement should still be confident, got %.2f", conf)
	}
}
