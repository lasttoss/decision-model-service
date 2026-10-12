// Package labelset defines the one thing this service is allowed to say. The label set is
// closed and small on purpose: a service that can answer anything cannot be evaluated,
// and an evaluable answer is the whole point of this repository.
package labelset

import (
	"errors"
	"fmt"
	"strings"
)

type Label string

const (
	// Retryable: a transient condition. Doing nothing and trying again later is correct.
	Retryable Label = "retryable"
	// NeedsHuman: it will not fix itself. Someone has to read it.
	NeedsHuman Label = "needs_human"
	// Expected: normal operation that happens to look like an error in a log line.
	Expected Label = "expected"
	// Abstain is not a prediction. It is the service saying the confidence is too low to
	// be useful, and it is reported separately rather than counted as a wrong answer:
	// a system that guesses on everything is worse than one that admits the gap.
	Abstain Label = "abstain"
)

var ErrUnknown = errors.New("unknown label")

func All() []Label { return []Label{Retryable, NeedsHuman, Expected} }

func Parse(s string) (Label, error) {
	switch Label(strings.ToLower(strings.TrimSpace(s))) {
	case Retryable, NeedsHuman, Expected, Abstain:
		return Label(strings.ToLower(strings.TrimSpace(s))), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknown, s)
}

// ScoredLabels are the labels a provider may predict. Abstain is a decision the service
// makes about a prediction, never something a provider returns.
func IsPredictable(l Label) bool { return l != Abstain }
