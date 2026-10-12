package evaluate

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lasttoss/decision-model-service/internal/cases"
	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
	"github.com/lasttoss/decision-model-service/internal/model/recorded"
	"github.com/lasttoss/decision-model-service/internal/rules"
	"github.com/lasttoss/decision-model-service/internal/score"
)

func loadCases(t *testing.T) []cases.Case {
	t.Helper()
	cs, err := cases.Load(filepath.Join("..", "..", "eval", "cases.jsonl"))
	if err != nil {
		t.Fatalf("load cases: %v", err)
	}
	return cs
}

func baseline() score.Report {
	return score.Report{
		N: 128, Hard: 13, HardCorrect: 7,
		Accuracy: 0.61, AccuracyWhenCommitted: 0.66, MacroF1: 0.60, Coverage: 0.92,
		CostPer1kDecisions: 0.0004,
	}
}

// The rule baseline is deterministic, so two runs must agree exactly. If this ever fails,
// something in the pipeline is not deterministic and no baseline is meaningful.
func TestRunOverTheFrozenSetIsDeterministic(t *testing.T) {
	cs := loadCases(t)
	first, samples := Run(context.Background(), rules.New(), cs, Options{Threshold: 0.5})
	second, _ := Run(context.Background(), rules.New(), cs, Options{Threshold: 0.5})

	if first.N != len(cs) || len(samples) != len(cs) {
		t.Fatalf("every case must be decided: %d of %d", first.N, len(cs))
	}
	if first.MacroF1 != second.MacroF1 || first.AccuracyWhenCommitted != second.AccuracyWhenCommitted {
		t.Fatalf("two runs differ: %+v vs %+v", first, second)
	}
	if first.Hard == 0 {
		t.Fatal("the case set must contain hard cases, or the hard metric measures nothing")
	}
	if first.Coverage > 1 || first.Coverage < 0 || first.MacroF1 > 1 {
		t.Fatalf("metrics must be proportions: %+v", first)
	}
	if !EqualRounded(first.MacroF1, second.MacroF1, 12) {
		t.Fatal("floating point comparison helper disagrees with equality")
	}
}

// The model path, end to end, with no network: the recorded provider reads the same
// envelopes the endpoint would return and runs the production parser over them.
func TestRunThroughRecordedModelResponses(t *testing.T) {
	provider, err := recorded.Load(filepath.Join("..", "..", "testdata", "model-envelopes.synthetic.jsonl"))
	if err != nil {
		t.Fatalf("load recordings: %v", err)
	}
	cs := []cases.Case{
		{ID: "r1", Input: "context canceled", Label: labelset.Expected},
		{ID: "r2", Input: "panic: runtime error: nil pointer dereference", Label: labelset.NeedsHuman},
		{ID: "r3", Input: "dial tcp 10.4.2.9:5432: i/o timeout", Label: labelset.Retryable},
		{ID: "r4", Input: "timeout talking to the secrets backend: permission denied", Label: labelset.NeedsHuman},
		{ID: "r5", Input: "the frobnicator wobbled twice", Label: labelset.Expected},
	}
	report, samples := Run(context.Background(), provider, cs, Options{Threshold: 0.5, PriceIn: 3, PriceOut: 15})

	if report.N != 5 {
		t.Fatalf("want 5 decisions, got %d", report.N)
	}
	// Four recorded answers are decisions; the fifth is prose, so it must be counted as a
	// degradation rather than quietly dropped or silently accepted.
	if report.Degraded["provider_unparseable"] != 1 {
		t.Fatalf("the unparseable recording must be counted as such: %+v", report.Degraded)
	}
	if report.TokensIn == 0 || report.TokensOut == 0 {
		t.Fatalf("usage must flow from the recording into the report: %+v", report)
	}

	// Three of the four parseable recordings agree with the label; the model is wrong on
	// the secrets-backend line in this fixture? No - it was recorded as needs_human, so
	// the point of the check is simply that the labels travel through unchanged.
	byInput := map[string]score.Sample{}
	for i, s := range samples {
		byInput[cs[i].Input] = s
	}
	if got := byInput["panic: runtime error: nil pointer dereference"].Outcome.Label; got != labelset.NeedsHuman {
		t.Fatalf("the recorded label must survive: got %s", got)
	}
	if got := byInput["the frobnicator wobbled twice"].Outcome.Source; got != "rules" && got != "none" {
		// With a single provider there is no fallback, so the outcome carries no source
		// from a model call that failed.
		t.Logf("source on the unparseable case: %s (reason %s)", got, byInput["the frobnicator wobbled twice"].Outcome.DegradedReason)
	}
}

// The proof that the gate can fail: a baseline is doctored upward for each metric in turn
// and the drop must be reported. Without this test the gate is a claim, not a gate.
func TestCompareCatchesADropInEveryMetric(t *testing.T) {
	base := baseline()
	drops := []struct {
		metric string
		mutate func(*score.Report)
	}{
		{"macro_f1", func(r *score.Report) { r.MacroF1 -= 0.05 }},
		{"accuracy_when_committed", func(r *score.Report) { r.AccuracyWhenCommitted -= 0.05 }},
		{"coverage", func(r *score.Report) { r.Coverage -= 0.05 }},
		{"hard_accuracy", func(r *score.Report) { r.HardCorrect -= 3 }},
		{"cost_per_1k_decisions_usd", func(r *score.Report) { r.CostPer1kDecisions += 0.5 }},
	}
	for _, d := range drops {
		current := base
		d.mutate(&current)
		regs := Compare(base, current, DefaultTolerance)
		found := false
		for _, reg := range regs {
			if reg.Metric == d.metric {
				found = true
			}
		}
		if !found {
			t.Errorf("a drop in %s was not reported: %+v", d.metric, regs)
		}
	}
}

func TestCompareIgnoresNoiseAndImprovements(t *testing.T) {
	base := baseline()

	noise := base
	noise.MacroF1 += 0.004
	noise.CostPer1kDecisions -= 0.0001
	if regs := Compare(base, noise, DefaultTolerance); len(regs) != 0 {
		t.Fatalf("noise inside the tolerance must not fail the gate: %+v", regs)
	}

	better := base
	better.MacroF1 += 0.2
	better.AccuracyWhenCommitted += 0.2
	better.HardCorrect += 5
	better.CostPer1kDecisions -= 0.0002
	if regs := Compare(base, better, DefaultTolerance); len(regs) != 0 {
		t.Fatalf("an improvement must not fail the gate: %+v", regs)
	}
}

// A baseline from a different case set is not a weaker comparison, it is a meaningless
// one, so it is refused on its own rather than reported as a percentage drop.
func TestCompareRefusesADifferentCaseSet(t *testing.T) {
	changed := baseline()
	changed.N += 10
	regs := Compare(baseline(), changed, DefaultTolerance)
	if len(regs) != 1 || regs[0].Metric != "n" {
		t.Fatalf("a different case count must be reported alone: %+v", regs)
	}
	if !strings.Contains(regs[0].Note, "case set changed") {
		t.Fatalf("the refusal must explain itself: %+v", regs[0])
	}
}

func TestReportRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	want := baseline()
	if err := SaveReport(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadReport(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.N != want.N || !EqualRounded(got.MacroF1, want.MacroF1, 9) {
		t.Fatalf("round trip changed the report: %+v vs %+v", got, want)
	}
	if _, err := LoadReport(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing baseline must be an error rather than an empty report")
	}
}

// A provider that fails everywhere must still produce a scored run: a broken provider is a
// result, not an exception.
func TestRunWithAFailingProviderStillScores(t *testing.T) {
	cs := loadCases(t)
	report, samples := Run(context.Background(), brokenProvider{}, cs, Options{Threshold: 0.5})
	if report.N != len(cs) || len(samples) != len(cs) {
		t.Fatalf("a failing provider must still produce a scored run: n=%d of %d", report.N, len(cs))
	}
	// The reason names the failure that was last seen. For a provider that fails outright
	// that is provider_error; no_provider_answered is reserved for the case where the
	// service was configured with no providers at all.
	if report.Degraded["provider_error"] != len(cs) {
		t.Fatalf("every case must show the outage: %+v", report.Degraded)
	}
	if report.Coverage != 0 {
		t.Fatalf("with nothing answering, coverage collapses: %.2f", report.Coverage)
	}
}

type brokenProvider struct{}

func (brokenProvider) Name() string { return "broken" }

func (brokenProvider) Predict(context.Context, string) (decision.Prediction, error) {
	return decision.Prediction{}, errors.New("provider is down")
}
