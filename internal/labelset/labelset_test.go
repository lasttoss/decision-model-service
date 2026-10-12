package labelset

import (
	"errors"
	"testing"
)

func TestParseAcceptsTheClosedSet(t *testing.T) {
	for _, in := range []string{"retryable", "RETRYABLE", " needs_human ", "expected", "abstain"} {
		if _, err := Parse(in); err != nil {
			t.Errorf("Parse(%q) rejected a known label: %v", in, err)
		}
	}
}

func TestParseRejectsAnythingElse(t *testing.T) {
	// The rejection matters more than the acceptance: a provider that answers "maybe"
	// must not be silently mapped onto a label, because that is how a service starts
	// inventing answers.
	for _, in := range []string{"", "maybe", "retry", "needs-human", "ok"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		} else if !errors.Is(err, ErrUnknown) {
			t.Errorf("Parse(%q) should wrap ErrUnknown, got %v", in, err)
		}
	}
}

func TestAbstainIsNotPredictable(t *testing.T) {
	if IsPredictable(Abstain) {
		t.Fatal("abstain must never be a provider's answer")
	}
	for _, l := range All() {
		if !IsPredictable(l) {
			t.Errorf("%s must be predictable", l)
		}
	}
	if len(All()) != 3 {
		t.Fatalf("the label set is closed at three labels, has %d", len(All()))
	}
}
