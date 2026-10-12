// Package rules is the baseline: a weighted pattern table, no model, no network, no
// configuration. It exists for two reasons - it answers when the model is unavailable,
// and it gives the evaluation something honest to compare against. A model that cannot
// beat this table on this task is not worth deploying, and without the table nobody
// would know.
package rules

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
)

type rule struct {
	re     *regexp.Regexp
	label  labelset.Label
	weight float64
}

// Weights are not decoration. A phrase that means exactly one thing ("permission denied")
// outweighs a phrase that appears in several situations ("timeout"), so an input that
// contains both lands on the more specific reading. The evaluation contains such inputs
// on purpose: they are where a keyword baseline is supposed to fail.
var table = []struct {
	pattern string
	label   labelset.Label
	weight  float64
}{
	// Transient: retrying later is the right action.
	{`(?i)\bdeadline exceeded\b`, labelset.Retryable, 1.5},
	{`(?i)\bi/o timeout\b`, labelset.Retryable, 1.5},
	// Two spellings, not two patterns for one word: `timed? ?out` also matches "timeout",
	// which double-counted the same evidence and quietly outvoted a more specific signal.
	{`(?i)\btimed out\b`, labelset.Retryable, 1.0},
	{`(?i)\btimeout\b`, labelset.Retryable, 1.0},
	{`(?i)\bconnection reset\b`, labelset.Retryable, 1.5},
	{`(?i)\bconnection refused\b`, labelset.Retryable, 1.2},
	{`(?i)\bbroken pipe\b`, labelset.Retryable, 1.2},
	{`(?i)\bunexpected EOF\b`, labelset.Retryable, 1.2},
	{`(?i)\btemporarily unavailable\b`, labelset.Retryable, 1.3},
	{`(?i)\btoo many requests\b`, labelset.Retryable, 1.3},
	{`(?i)\b(429|502|503|504)\b`, labelset.Retryable, 1.0},
	{`(?i)\bleader election\b`, labelset.Retryable, 1.0},
	{`(?i)\bwill retry\b`, labelset.Retryable, 1.0},
	{`(?i)\bconnection pool exhausted\b`, labelset.Retryable, 1.2},

	// Will not fix itself: a person has to read it.
	{`(?i)\bpermission denied\b`, labelset.NeedsHuman, 2.0},
	{`(?i)\bunauthorized\b`, labelset.NeedsHuman, 1.8},
	{`(?i)\bforbidden\b`, labelset.NeedsHuman, 1.6},
	{`(?i)\binvalid credentials\b`, labelset.NeedsHuman, 1.8},
	{`(?i)\b(401|403)\b`, labelset.NeedsHuman, 1.4},
	{`(?i)\bpanic\b`, labelset.NeedsHuman, 2.0},
	{`(?i)\bnil pointer\b`, labelset.NeedsHuman, 2.0},
	{`(?i)\bsegmentation fault\b`, labelset.NeedsHuman, 2.0},
	{`(?i)\bout of memory\b`, labelset.NeedsHuman, 1.6},
	{`(?i)\boomkill\b`, labelset.NeedsHuman, 1.6},
	{`(?i)\bno space left\b`, labelset.NeedsHuman, 1.8},
	{`(?i)\bchecksum mismatch\b`, labelset.NeedsHuman, 2.0},
	{`(?i)\binvariant violated\b`, labelset.NeedsHuman, 2.0},
	{`(?i)\bcould not deserialize\b`, labelset.NeedsHuman, 1.5},
	{`(?i)\bmigration\b`, labelset.NeedsHuman, 1.2},
	{`(?i)\bschema mismatch\b`, labelset.NeedsHuman, 1.8},
	{`(?i)\bquota exceeded\b`, labelset.NeedsHuman, 1.4},

	// Normal operation that looks like an error. The probe lines are deliberately light:
	// a probe that reports a real dependency failure is labelled by the failure, not by
	// the word "probe", and the evaluation contains both shapes.

	{`(?i)\bcontext canceled\b`, labelset.Expected, 2.0},
	{`(?i)\bcancell?ed by (the )?client\b`, labelset.Expected, 2.0},
	{`(?i)\bclient (disconnected|hung up)\b`, labelset.Expected, 2.0},
	{`(?i)\b(readiness|liveness) probe\b`, labelset.Expected, 1.0},
	{`(?i)\bhealth ?check\b`, labelset.Expected, 1.6},
	{`(?i)\bshutting down\b`, labelset.Expected, 1.6},
	{`(?i)\bgraceful shutdown\b`, labelset.Expected, 2.0},
	{`(?i)\breloaded config\b`, labelset.Expected, 1.6},
	{`(?i)\breconnect(ed|ing)?\b`, labelset.Expected, 1.2},
	{`(?i)\bdeployment rollout\b`, labelset.Expected, 1.4},
}

// noMatchLabel and noMatchConfidence are the honest answer to "nothing matched". The
// label is needs_human because an unrecognised message is exactly the case a person
// should look at; the confidence is low enough that the default threshold abstains, so
// the service does not pretend to know.
const (
	noMatchLabel      = labelset.NeedsHuman
	noMatchConfidence = 0.30
)

// MaxConfidence is the ceiling for a matched rule. A pattern table is never certain, and
// reporting 0.99 from it would be the kind of number that hides a threshold misconfigured
// at 0.95.
const MaxConfidence = 0.97

type Provider struct {
	rules []rule
}

func New() *Provider {
	p := &Provider{}
	for _, r := range table {
		p.rules = append(p.rules, rule{re: regexp.MustCompile(r.pattern), label: r.label, weight: r.weight})
	}
	return p
}

func (p *Provider) Name() string { return "rules" }

func (p *Provider) Predict(_ context.Context, input string) (decision.Prediction, error) {
	scores := map[labelset.Label]float64{}
	var matched []string
	for _, r := range p.rules {
		if r.re.MatchString(input) {
			scores[r.label] += r.weight
			matched = append(matched, fmt.Sprintf("%s(+%.1f %s)", r.re.String(), r.weight, r.label))
		}
	}

	total := 0.0
	for _, v := range scores {
		total += v
	}
	if total == 0 {
		return decision.Prediction{
			Label:      noMatchLabel,
			Confidence: noMatchConfidence,
			Source:     p.Name(),
			Detail:     "no pattern matched",
		}, nil
	}

	// Highest score wins. A tie is broken toward the more cautious label - needs_human
	// before retryable before expected - and that order is written out rather than
	// inherited from labelset.All(), because a tie decided by the order of a slice is not
	// a decision anyone can review. In a triage tool the cautious direction is the right
	// default: asking a person to look at something that was fine costs a minute, and the
	// other mistake costs an incident.
	winner, best := labelset.Label(""), -1.0
	for _, label := range []labelset.Label{labelset.NeedsHuman, labelset.Retryable, labelset.Expected} {
		if scores[label] > best {
			winner, best = label, scores[label]
		}
	}
	conf := best / total
	if conf > MaxConfidence {
		conf = MaxConfidence
	}
	sort.Strings(matched)
	return decision.Prediction{
		Label:      winner,
		Confidence: conf,
		Source:     p.Name(),
		Detail:     strings.Join(matched, " "),
	}, nil
}
