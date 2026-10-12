// Package score turns a list of outcomes into numbers. It is deliberately strict about
// what the numbers mean, because the most common way an evaluation lies is not by
// miscalculating - it is by counting an abstention as an answer.
//
// The rules here:
//   - a committed prediction (not abstained) is scored for accuracy and F1;
//   - an abstention is not scored as wrong: it is counted, and reported as coverage;
//   - every label is reported separately, because macro-F1 over three labels hides the
//     case where one label is never predicted;
//   - ties and empty classes are handled explicitly rather than producing NaN.
package score

import (
	"sort"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
)

type Sample struct {
	Want    labelset.Label
	Outcome decision.Outcome
	// Hard marks the inputs where the obvious keyword reading is wrong, so the report can
	// state the model's score on them separately. A single headline number over a set
	// that is mostly easy is the most common way an evaluation flatters itself.
	Hard bool
}

type LabelScore struct {
	Support   int     `json:"support"`
	Predicted int     `json:"predicted"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

type Report struct {
	N                     int                           `json:"n"`
	Hard                  int                           `json:"hard"`
	HardCorrect           int                           `json:"hard_correct"`
	Accuracy              float64                       `json:"accuracy"`
	AccuracyWhenCommitted float64                       `json:"accuracy_when_committed"`
	MacroF1               float64                       `json:"macro_f1"`
	Coverage              float64                       `json:"coverage"`
	Abstained             int                           `json:"abstained"`
	Committed             int                           `json:"committed"`
	PerLabel              map[labelset.Label]LabelScore `json:"per_label"`
	// Confusion[want][got], where got may be "abstain". It is reported rather than only
	// summarised because the interesting failures are directional: this task's expensive
	// mistake is calling something needs_human retryable, and only the matrix shows it.
	Confusion          map[labelset.Label]map[string]int `json:"confusion"`
	Degraded           map[string]int                    `json:"degraded_by_reason"`
	LatencyP50MS       float64                           `json:"latency_p50_ms"`
	LatencyP95MS       float64                           `json:"latency_p95_ms"`
	TokensIn           int                               `json:"tokens_in"`
	TokensOut          int                               `json:"tokens_out"`
	CostPer1kDecisions float64                           `json:"cost_per_1k_decisions_usd"`
	PricePerMTokIn     float64                           `json:"price_per_mtok_in_usd"`
	PricePerMTokOut    float64                           `json:"price_per_mtok_out_usd"`
}

type Options struct {
	PricePerMTokIn  float64
	PricePerMTokOut float64
}

func Compute(samples []Sample, opts Options) Report {
	r := Report{
		N:               len(samples),
		PerLabel:        map[labelset.Label]LabelScore{},
		Confusion:       map[labelset.Label]map[string]int{},
		Degraded:        map[string]int{},
		PricePerMTokIn:  opts.PricePerMTokIn,
		PricePerMTokOut: opts.PricePerMTokOut,
	}
	for _, l := range labelset.All() {
		r.Confusion[l] = map[string]int{}
	}

	var latencies []float64
	for _, s := range samples {
		lat := r.PerLabel[s.Want]
		lat.Support++
		r.PerLabel[s.Want] = lat
		// Only real reasons are counted: an empty reason is the normal case, and a bucket
		// for it turns "how often did we degrade, and why" into a table with a mystery row.
		if s.Outcome.DegradedReason != "" {
			r.Degraded[s.Outcome.DegradedReason]++
		}
		// The decision's total time, not the provider's: a fallback that took 800ms and
		// then answered in 1ms cost the caller 801ms, and that is the number that decides
		// whether this can sit in a request path.
		if s.Outcome.TotalMS > 0 {
			latencies = append(latencies, s.Outcome.TotalMS)
		}

		got := string(s.Outcome.Label)
		if s.Outcome.Abstained {
			r.Abstained++
			got = string(labelset.Abstain)
		} else {
			r.Committed++
			scored := r.PerLabel[s.Want]
			scored.Predicted++ // placeholder; replaced below by prediction counts
			r.PerLabel[s.Want] = scored
		}
		r.Confusion[s.Want][got]++
	}
	// Prediction counts are per predicted label, not per wanted label, so they are
	// counted in a second pass rather than folded into the loop above where it would be
	// easy to confuse the two.
	predCount := map[labelset.Label]int{}
	for _, s := range samples {
		if s.Outcome.Abstained {
			continue
		}
		predCount[s.Outcome.Label]++
	}

	correct, committedCorrect := 0, 0
	for _, s := range samples {
		if s.Hard {
			r.Hard++
		}
		if s.Outcome.Abstained {
			continue
		}
		if s.Outcome.Label == s.Want {
			correct++
			committedCorrect++
			if s.Hard {
				r.HardCorrect++
			}
		}
	}
	r.Accuracy = ratio(correct, r.N)
	r.AccuracyWhenCommitted = ratio(committedCorrect, r.Committed)
	r.Coverage = ratio(r.Committed, r.N)

	f1sum, labels := 0.0, 0
	for _, l := range labelset.All() {
		ls := r.PerLabel[l]
		ls.Predicted = predCount[l]
		tp := 0
		for _, s := range samples {
			if s.Want == l && !s.Outcome.Abstained && s.Outcome.Label == l {
				tp++
			}
		}
		ls.Precision = ratio(tp, ls.Predicted)
		ls.Recall = ratio(tp, ls.Support)
		if ls.Precision+ls.Recall > 0 {
			ls.F1 = 2 * ls.Precision * ls.Recall / (ls.Precision + ls.Recall)
		}
		r.PerLabel[l] = ls
		// A label with no support in the set is excluded from macro-F1 rather than
		// counted as zero: the case set is small, and punishing the model for a label the
		// set does not contain would be measuring the file.
		if ls.Support > 0 {
			f1sum += ls.F1
			labels++
		}
	}
	if labels > 0 {
		r.MacroF1 = f1sum / float64(labels)
	}

	sort.Float64s(latencies)
	r.LatencyP50MS = percentile(latencies, 0.50)
	r.LatencyP95MS = percentile(latencies, 0.95)

	for _, s := range samples {
		r.TokensIn += s.Outcome.TokensIn
		r.TokensOut += s.Outcome.TokensOut
	}
	if r.N > 0 {
		cost := float64(r.TokensIn)/1e6*r.PricePerMTokIn + float64(r.TokensOut)/1e6*r.PricePerMTokOut
		r.CostPer1kDecisions = cost / float64(r.N) * 1000
	}
	return r
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// percentile is nearest-rank on an already sorted slice: p95 is the value at ceil(0.95*n),
// not an interpolation. An interpolated latency that no request ever experienced is a
// number that cannot be reproduced from the raw data.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
