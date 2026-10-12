package main

import (
	"strings"
	"testing"
	"time"

	"github.com/lasttoss/decision-model-service/internal/cases"
	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
	"github.com/lasttoss/decision-model-service/internal/score"
)

// What a reader of the evaluation actually sees is this rendering, so the tests here assert
// the properties the repository claims rather than the exact layout: the honest limits sit
// next to the headline, a label the set does not contain is not printed as a row of zeroes,
// and a mistake says why it is surprising when the case is a hard one.

func lineStartingWith(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestRenderReportPutsTheLimitsNextToTheHeadline(t *testing.T) {
	out := renderReport(score.Report{
		N: 10, Hard: 4, HardCorrect: 3,
		Accuracy: 0.7, AccuracyWhenCommitted: 0.875, MacroF1: 0.8,
		Coverage: 0.8, Abstained: 2, Committed: 8,
		PerLabel: map[labelset.Label]score.LabelScore{
			labelset.Retryable: {Support: 5, Predicted: 5, Precision: 0.8, Recall: 0.8, F1: 0.8},
			labelset.Expected:  {Support: 0, Predicted: 0},
		},
		Confusion: map[labelset.Label]map[string]int{
			labelset.Retryable: {"retryable": 4, "needs_human": 1},
		},
		Degraded:           map[string]int{"provider_timeout": 2, "provider_unparseable": 1},
		LatencyP50MS:       12.5,
		LatencyP95MS:       40,
		CostPer1kDecisions: 0.75, PricePerMTokIn: 3, PricePerMTokOut: 15,
	})
	for _, want := range []string{
		"macro_f1", "0.8000",
		"0.8750  (over the 8 it committed to)",
		"0.8000  (2 of 10 abstained)",
		"0.7500  (3 of 4)",
		"0.7500 $/1k decisions (price 3.00/15.00 per Mtok)",
		"p50 12.50ms  p95 40.00ms",
		"provider_timeout=2", "provider_unparseable=1",
		"needs_human:1", "retryable:4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not contain %q:\n%s", want, out)
		}
	}
	// A label with no support and no prediction is left out: a row of zeroes reads like a
	// label that was measured and scored badly.
	if strings.Contains(out, string(labelset.Expected)) {
		t.Errorf("the report printed a row for a label with no support:\n%s", out)
	}
}

func TestRenderReportOmitsHardAccuracyWhenTheSetHasNoHardCases(t *testing.T) {
	out := renderReport(score.Report{N: 3, Committed: 3, AccuracyWhenCommitted: 1,
		PerLabel: map[labelset.Label]score.LabelScore{}})
	if strings.Contains(out, "hard_accuracy") {
		t.Errorf("a set with no hard cases reported a hard_accuracy:\n%s", out)
	}
	if !strings.Contains(out, "none") {
		t.Errorf("a run with nothing degraded should say so:\n%s", out)
	}
}

func TestRenderRunHeaderNamesEveryLabelEvenAtZero(t *testing.T) {
	out := renderRunHeader(evalConfig{provider: "rules", threshold: 0.6}, 10, 3,
		map[labelset.Label]int{labelset.Retryable: 10})

	if got := lineStartingWith(out, "provider"); !strings.Contains(got, "rules") {
		t.Errorf("the header does not name the provider: %q", got)
	}
	if got := lineStartingWith(out, "threshold"); !strings.Contains(got, "0.60") {
		t.Errorf("the header does not print the threshold: %q", got)
	}
	if got := lineStartingWith(out, "cases"); !strings.Contains(got, "10, of which 3 are hard") {
		t.Errorf("the header does not print the set's shape: %q", got)
	}
	// The label row is printed for every label the set is allowed to contain, so a set that
	// has drifted to one label shows up as a row of zeroes rather than as a strange F1.
	labels := lineStartingWith(out, "case labels")
	for _, want := range []string{"retryable=10", "needs_human=0", "expected=0"} {
		if !strings.Contains(labels, want) {
			t.Errorf("the label row does not contain %q: %q", want, labels)
		}
	}
}

func TestRenderMistakesShowsTheWhyOnlyForHardCasesAndStopsAtTheLimit(t *testing.T) {
	cs := []cases.Case{
		{ID: "easy-right", Input: "connection reset", Label: labelset.Retryable},
		{ID: "hard-wrong", Input: "the customer asked for a refund", Label: labelset.NeedsHuman, Hard: true,
			Why: "a refund is a policy decision, so the keyword reading is wrong"},
		{ID: "abstained", Input: "unclear", Label: labelset.Retryable},
	}
	samples := []score.Sample{
		{Want: labelset.Retryable, Outcome: decision.Outcome{
			Prediction: decision.Prediction{Label: labelset.Retryable, Confidence: 0.9}}},
		{Want: labelset.NeedsHuman, Outcome: decision.Outcome{
			Prediction: decision.Prediction{Label: labelset.Retryable, Confidence: 0.7}}},
		{Want: labelset.Retryable, Outcome: decision.Outcome{
			Prediction: decision.Prediction{Label: labelset.Retryable, Confidence: 0.4}, Abstained: true}},
	}

	out := renderMistakes(cs, samples, 2)
	if !strings.Contains(out, "hard-wrong want=needs_human got=retryable conf=0.70 [hard]") {
		t.Errorf("the hard mistake is not reported with its label and confidence:\n%s", out)
	}
	if !strings.Contains(out, "why:") {
		t.Errorf("a hard mistake must print the reason it is surprising:\n%s", out)
	}
	if strings.Contains(out, "easy-right") || strings.Contains(out, "abstained") {
		t.Errorf("a correct answer and an abstention are not mistakes:\n%s", out)
	}

	// A limit of zero is what a run without -mistakes asks for, and it says none rather than
	// printing a heading with nothing under it.
	if got := renderMistakes(cs, samples, 0); !strings.Contains(got, "none") {
		t.Errorf("a limit of zero printed something other than none:\n%s", got)
	}
}

func TestRenderMistakesShortensALongInputRatherThanPrintingItWhole(t *testing.T) {
	long := strings.Repeat("the customer ", 20)
	out := renderMistakes(
		[]cases.Case{{ID: "long", Input: long, Label: labelset.Expected}},
		[]score.Sample{{Want: labelset.Expected, Outcome: decision.Outcome{
			Prediction: decision.Prediction{Label: labelset.Retryable}}}},
		1)
	if !strings.Contains(out, "…") {
		t.Errorf("a long input is not clipped:\n%s", out)
	}
	if strings.Contains(out, long) {
		t.Errorf("the whole input was printed instead of a clipped one")
	}
}

func TestProviderChainForRulesIsOneProviderAndAnUnknownNameIsAnError(t *testing.T) {
	chain, err := providerChain("rules", "", "", "", "", time.Second)
	if err != nil || len(chain) != 1 {
		t.Fatalf("providerChain(rules) = %d providers, %v; want 1 and no error", len(chain), err)
	}
	if _, err := providerChain("nonsense", "", "", "", "", time.Second); err == nil {
		t.Fatalf("providerChain(nonsense) should fail")
	} else if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("the error does not name the provider that was asked for: %v", err)
	}
}

func TestEnvOrFallsBackOnlyWhenTheVariableIsUnset(t *testing.T) {
	t.Setenv("DECIDE_TEST_VALUE", "")
	if got := envOr("DECIDE_TEST_VALUE", "fallback"); got != "fallback" {
		t.Errorf("envOr on an empty variable = %q, want the fallback", got)
	}
	t.Setenv("DECIDE_TEST_VALUE", "set")
	if got := envOr("DECIDE_TEST_VALUE", "fallback"); got != "set" {
		t.Errorf("envOr on a set variable = %q, want it", got)
	}
}
