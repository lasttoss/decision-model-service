package metrics

import (
	"strings"
	"testing"
)

func TestCountersAddUp(t *testing.T) {
	c := New()
	c.AddDecision(false, true)
	c.AddDecision(true, false)
	c.AddDecision(false, false)
	c.AddDegraded("provider_timeout")
	c.AddDegraded("provider_timeout")
	c.AddDegraded("provider_unparseable")
	c.AddExplainFailure()

	s := c.Snapshot()
	if s.Decisions != 3 || s.Abstained != 1 || s.Explained != 1 {
		t.Fatalf("counts wrong: %+v", s)
	}
	out := Render(s)
	for _, want := range []string{
		"decision_decisions_total 3",
		"decision_abstained_total 1",
		"decision_degraded_total{reason=\"provider_timeout\"} 2",
		"decision_degraded_total{reason=\"provider_unparseable\"} 1",
		"decision_explained_total 1",
		"decision_explain_failures_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output is missing %q:\n%s", want, out)
		}
	}
}

func TestRenderIsStableAndLabelled(t *testing.T) {
	s := Snapshot{Degraded: map[string]int64{"b": 1, "a": 2}}
	first, second := Render(s), Render(s)
	if first != second {
		t.Fatal("rendering the same snapshot twice must produce identical text")
	}
	if strings.Index(first, "reason=\"a\"") > strings.Index(first, "reason=\"b\"") {
		t.Fatal("label values must be sorted so the output is diffable")
	}
	if !strings.Contains(first, "# TYPE decision_degraded_total counter") {
		t.Fatal("every counter needs a TYPE line")
	}
	// An empty family still emits a zero series, so a scraper can tell "no degradations"
	// from "this counter does not exist".
	if !strings.Contains(Render(Snapshot{}), "decision_degraded_total{reason=\"none\"} 0") {
		t.Fatal("an empty degraded family must still be emitted")
	}
}
