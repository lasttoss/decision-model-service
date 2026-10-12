// Package evaluate runs a provider over the frozen case set and compares the result with a
// committed baseline. The comparison is the part that matters: a score nobody compares
// against anything is a number that drifts quietly, and a gate that has never been seen to
// fail is not a gate. There is a test for exactly that.
package evaluate

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/lasttoss/decision-model-service/internal/cases"
	"github.com/lasttoss/decision-model-service/internal/decide"
	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/score"
)

type Options struct {
	Threshold float64
	Explain   bool
	PriceIn   float64
	PriceOut  float64
}

// Run decides every case and scores the outcomes. It returns the samples as well as the
// report so a caller can print individual mistakes without deciding everything twice.
func Run(ctx context.Context, provider decision.Provider, cs []cases.Case, opts Options) (score.Report, []score.Sample) {
	service := &decide.Service{Providers: []decision.Provider{provider}, Threshold: opts.Threshold, Explain: opts.Explain}

	samples := make([]score.Sample, 0, len(cs))
	for _, c := range cs {
		out := service.Decide(ctx, c.Input)
		samples = append(samples, score.Sample{Want: c.Label, Outcome: out, Hard: c.Hard})
	}
	return score.Compute(samples, score.Options{PricePerMTokIn: opts.PriceIn, PricePerMTokOut: opts.PriceOut}), samples
}

// DefaultTolerance is deliberately not zero: floating point sums over a few hundred cases
// move in the last bits, and a gate that fails on that teaches people to re-run it.
const DefaultTolerance = 0.01

type Regression struct {
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	Delta    float64 `json:"delta"`
	Note     string  `json:"note,omitempty"`
}

// Compared metrics. Each one is a thing this repository claims, so a drop in any of them is
// a claim that quietly stopped being true - which is the entire reason the gate exists.
func Compare(baseline, current score.Report, tolerance float64) []Regression {
	var out []Regression

	// The case set is the ruler. If it changed size, every number below is measuring a
	// different thing, and reporting a percentage would be worse than reporting nothing.
	if baseline.N != current.N {
		return []Regression{{
			Metric: "n", Baseline: float64(baseline.N), Current: float64(current.N),
			Delta: float64(current.N - baseline.N),
			Note:  "the case set changed size, so no metric is comparable: update the baseline deliberately",
		}}
	}

	type metric struct {
		name              string
		baseline, current float64
	}
	metrics := []metric{
		{"macro_f1", baseline.MacroF1, current.MacroF1},
		{"accuracy_when_committed", baseline.AccuracyWhenCommitted, current.AccuracyWhenCommitted},
		{"coverage", baseline.Coverage, current.Coverage},
		{"hard_accuracy", hardRate(baseline), hardRate(current)},
		{"cost_per_1k_decisions_usd", baseline.CostPer1kDecisions, current.CostPer1kDecisions},
	}
	for _, m := range metrics {
		delta := m.current - m.baseline
		// Cost is the one metric where a rise is the regression.
		regressed := delta < -tolerance
		if m.name == "cost_per_1k_decisions_usd" {
			regressed = delta > tolerance
			delta = -delta
		}
		if !regressed {
			continue
		}
		out = append(out, Regression{Metric: m.name, Baseline: m.baseline, Current: m.current, Delta: delta})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metric < out[j].Metric })
	return out
}

func hardRate(r score.Report) float64 {
	if r.Hard == 0 {
		return 0
	}
	return float64(r.HardCorrect) / float64(r.Hard)
}

func SaveReport(path string, r score.Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func LoadReport(path string) (score.Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return score.Report{}, err
	}
	var r score.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return score.Report{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// EqualRounded exists so a test can assert that two runs agree without depending on the
// last bits of a float sum.
func EqualRounded(a, b float64, places int) bool {
	scale := math.Pow(10, float64(places))
	return math.Round(a*scale) == math.Round(b*scale)
}
