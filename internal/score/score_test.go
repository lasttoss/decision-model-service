package score

import (
	"math"
	"testing"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
)

func sample(want, got labelset.Label, abstained bool) Sample {
	return Sample{Want: want, Outcome: decision.Outcome{Prediction: decision.Prediction{Label: got}, Abstained: abstained}}
}

func close(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s: want %.4f, got %.4f", what, want, got)
	}
}

// Four samples, hand-computed, so the metric has one place where the arithmetic is not a
// matter of opinion: 2 right, 1 wrong, 1 abstained.
func TestHandComputedMetrics(t *testing.T) {
	samples := []Sample{
		sample(labelset.Retryable, labelset.Retryable, false),
		sample(labelset.Retryable, labelset.Retryable, false),
		sample(labelset.Retryable, labelset.NeedsHuman, false),
		sample(labelset.NeedsHuman, labelset.NeedsHuman, true), // abstained: not an answer
	}
	r := Compute(samples, Options{})

	if r.N != 4 || r.Committed != 3 || r.Abstained != 1 {
		t.Fatalf("counts wrong: n=%d committed=%d abstained=%d", r.N, r.Committed, r.Abstained)
	}
	close(t, r.Accuracy, 2.0/4.0, "accuracy over everything, abstentions included in the denominator")
	close(t, r.AccuracyWhenCommitted, 2.0/3.0, "accuracy over what it acted on")
	close(t, r.Coverage, 0.75, "coverage")

	// retryable: support 3, predicted 2, tp 2 -> precision 1.0, recall 2/3
	retry := r.PerLabel[labelset.Retryable]
	if retry.Support != 3 || retry.Predicted != 2 {
		t.Fatalf("retryable counts wrong: %+v", retry)
	}
	close(t, retry.Precision, 1.0, "retryable precision")
	close(t, retry.Recall, 2.0/3.0, "retryable recall")
	if r.PerLabel[labelset.Expected].Support != 0 {
		t.Fatal("a label absent from the set must have zero support")
	}
	// macro-F1 over labels that appear: retryable (f1 = 2*(1*2/3)/(1+2/3) = 0.8) and
	// needs_human (support 1, predicted 0 -> recall 0, f1 0) -> mean 0.4
	close(t, r.MacroF1, 0.4, "macro-F1")

	if got := r.Confusion[labelset.NeedsHuman][string(labelset.Abstain)]; got != 1 {
		t.Fatalf("the abstention must be visible in the matrix, got %d", got)
	}
	if got := r.Confusion[labelset.Retryable][string(labelset.NeedsHuman)]; got != 1 {
		t.Fatalf("the mistake must be visible in the matrix, got %d", got)
	}
}

// An abstention must not be scored as a wrong answer: that is the whole reason the metric
// reports coverage next to accuracy, and it is the mistake this test exists to prevent.
func TestAbstainingEverythingIsNotFree(t *testing.T) {
	samples := []Sample{
		sample(labelset.Retryable, labelset.Retryable, true),
		sample(labelset.NeedsHuman, labelset.NeedsHuman, true),
	}
	r := Compute(samples, Options{})
	close(t, r.Accuracy, 0, "abstaining is never correct")
	close(t, r.AccuracyWhenCommitted, 0, "there is nothing committed")
	close(t, r.Coverage, 0, "coverage collapses, which is the visible cost")
	if r.Abstained != 2 {
		t.Fatalf("abstentions must be counted, got %d", r.Abstained)
	}
}

func TestPercentileIsNearestRankNotInterpolation(t *testing.T) {
	// 100 values 1..100: p50 is the value at index 50 -> 51, p95 -> 96.
	var samples []Sample
	for i := 1; i <= 100; i++ {
		s := sample(labelset.Retryable, labelset.Retryable, false)
		s.Outcome.TotalMS = float64(i)
		samples = append(samples, s)
	}
	r := Compute(samples, Options{})
	close(t, r.LatencyP50MS, 51, "p50")
	close(t, r.LatencyP95MS, 96, "p95")
}

func TestCostIsPerThousandDecisions(t *testing.T) {
	samples := []Sample{}
	for i := 0; i < 10; i++ {
		s := sample(labelset.Retryable, labelset.Retryable, false)
		s.Outcome.TokensIn = 100
		s.Outcome.TokensOut = 2
		samples = append(samples, s)
	}
	// 1000 tokens in at $3/M + 20 out at $15/M = 0.003 + 0.0003 = $0.0033 for 10
	// decisions -> $0.33 per 1000 decisions.
	r := Compute(samples, Options{PricePerMTokIn: 3, PricePerMTokOut: 15})
	close(t, r.CostPer1kDecisions, 0.33, "cost per 1k")
	// The prices travel with the report, so a cost number can never be read without the
	// assumption it was computed from.
	if r.PricePerMTokIn != 3 {
		t.Fatal("the report must carry the price it used")
	}
}

func TestHardCasesAreReportedSeparately(t *testing.T) {
	easy := sample(labelset.Retryable, labelset.Retryable, false)
	hard := sample(labelset.Expected, labelset.NeedsHuman, false) // wrong, and it is a hard one
	hard.Hard = true
	r := Compute([]Sample{easy, hard}, Options{})
	if r.N != 2 || r.Hard != 1 || r.HardCorrect != 0 {
		t.Fatalf("hard cases must be counted on their own: %+v", r)
	}
	// The headline number and the hard number disagree here, which is the point of
	// reporting both.
	close(t, r.AccuracyWhenCommitted, 0.5, "overall")
}

func TestDegradedReasonsAreCounted(t *testing.T) {
	s1 := sample(labelset.Retryable, labelset.Retryable, false)
	s1.Outcome.Degraded = true
	s1.Outcome.DegradedReason = "model_timeout"
	s2 := sample(labelset.Retryable, labelset.Retryable, false)
	s2.Outcome.Degraded = true
	s2.Outcome.DegradedReason = "model_timeout"
	r := Compute([]Sample{s1, s2}, Options{})
	if r.Degraded["model_timeout"] != 2 {
		t.Fatalf("degraded reasons must be counted, got %+v", r.Degraded)
	}
}
