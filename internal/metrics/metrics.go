// Package metrics renders counters by hand, in the exposition format. No client library:
// there are six counters here, and a dependency would be a worse trade than sixty lines
// anyone can read in a review.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Snapshot struct {
	Decisions      int64            `json:"decisions"`
	Abstained      int64            `json:"abstained"`
	Degraded       map[string]int64 `json:"degraded_by_reason"`
	Explained      int64            `json:"explained"`
	ExplainsFailed int64            `json:"explains_failed"`
}

type Counters struct {
	mu       sync.Mutex
	snapshot Snapshot
}

func New() *Counters {
	return &Counters{snapshot: Snapshot{Degraded: map[string]int64{}}}
}

func (c *Counters) AddDecision(abstained, explained bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.Decisions++
	if abstained {
		c.snapshot.Abstained++
	}
	if explained {
		c.snapshot.Explained++
	}
}

func (c *Counters) AddDegraded(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.Degraded[reason]++
}

func (c *Counters) AddExplainFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.ExplainsFailed++
}

func (c *Counters) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Snapshot{
		Decisions: c.snapshot.Decisions, Abstained: c.snapshot.Abstained,
		Explained: c.snapshot.Explained, ExplainsFailed: c.snapshot.ExplainsFailed,
		Degraded: map[string]int64{},
	}
	for k, v := range c.snapshot.Degraded {
		out.Degraded[k] = v
	}
	return out
}

// Render writes the exposition format. Label values are sorted so that two runs of the
// same traffic produce identical text, which is what makes the output diffable.
func Render(s Snapshot) string {
	var b strings.Builder
	write := func(name, help, kind string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	}
	write("decision_decisions_total", "Decisions answered, counted once per request.", "counter")
	fmt.Fprintf(&b, "decision_decisions_total %d\n", s.Decisions)

	write("decision_abstained_total", "Decisions whose confidence was below the threshold.", "counter")
	fmt.Fprintf(&b, "decision_abstained_total %d\n", s.Abstained)

	write("decision_degraded_total", "Decisions answered by a fallback provider, by reason.", "counter")
	reasons := make([]string, 0, len(s.Degraded))
	for reason := range s.Degraded {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	if len(reasons) == 0 {
		// A labelled counter with no series is still worth emitting: a scraper that sees
		// an empty family knows the counter exists, rather than guessing from its absence.
		fmt.Fprintf(&b, "decision_degraded_total{reason=\"none\"} 0\n")
	}
	for _, reason := range reasons {
		fmt.Fprintf(&b, "decision_degraded_total{reason=%q} %d\n", reason, s.Degraded[reason])
	}

	write("decision_explained_total", "Decisions that carried a model-written explanation.", "counter")
	fmt.Fprintf(&b, "decision_explained_total %d\n", s.Explained)

	write("decision_explain_failures_total", "Explanations that failed. An explanation must never fail a decision.", "counter")
	fmt.Fprintf(&b, "decision_explain_failures_total %d\n", s.ExplainsFailed)
	return b.String()
}
