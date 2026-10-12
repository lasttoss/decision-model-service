package cases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lasttoss/decision-model-service/internal/labelset"
)

// write puts a case file where a test can read it. Load is the only way into a Case and it
// reports errors with a line number, so the tests below are the files a person could have
// written by hand: comments, blank lines, a typo, and a case that contradicts itself.
func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cases.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadReadsCommentsBlankLinesAndHardCases(t *testing.T) {
	path := write(t, `
# The case file carries its own explanation, and Load has to let it: the frozen set is the one
# thing here that cannot be regenerated from the code, so the reasoning belongs in the file.

{"id":"a","input":"connection reset by peer","label":"retryable"}
{"id":"b","input":"the customer asked for a refund","label":"needs_human","hard":true,"why":"a refund is a policy decision, not a retry"}
`)
	cs, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %d cases, want 2 (comments and blank lines are not cases)", len(cs))
	}
	if cs[0].ID != "a" || cs[0].Label != labelset.Retryable || cs[0].Hard {
		t.Errorf("first case = %+v, want a plain retryable", cs[0])
	}
	if cs[1].Label != labelset.NeedsHuman || !cs[1].Hard || cs[1].Why == "" {
		t.Errorf("second case = %+v, want a hard needs_human that says why", cs[1])
	}
}

func TestLoadRefusesTheFilesThatWouldScoreGarbage(t *testing.T) {
	// Every rejection here is a way a case set can be wrong without looking wrong. The one
	// that matters most is the last: a set that silently lost half its cases would report a
	// confident F1 over the half that survived.
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"no cases at all", "# only a comment\n\n", "no cases"},
		{"json that does not parse", `{"id":"a","input":}`, "cases.jsonl:3"},
		{"a field nobody defined", `{"id":"a","input":"x","label":"expected","severity":"high"}`, "unknown field"},
		{"no id", `{"id":"","input":"x","label":"expected"}`, "id is required"},
		{"empty input", `{"id":"a","input":"   ","label":"expected"}`, "input is empty"},
		{"a label outside the closed set", `{"id":"a","input":"x","label":"maybe"}`, "unknown label"},
		{"abstain used as a label", `{"id":"a","input":"x","label":"abstain"}`, "abstain is not a label a case can have"},
		{"hard with no reason", `{"id":"a","input":"x","label":"expected","hard":true}`, "must say why"},
		{
			"the same id twice",
			"{\"id\":\"a\",\"input\":\"x\",\"label\":\"expected\"}\n{\"id\":\"a\",\"input\":\"y\",\"label\":\"retryable\"}",
			"duplicate id a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Two leading blank lines, so the line numbers in the errors are the ones a
			// reader of the real file would see.
			_, err := Load(write(t, "\n\n"+tc.body))
			if err == nil {
				t.Fatalf("Load accepted a file it should refuse")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadOnAMissingFileIsAnErrorRatherThanAnEmptySet(t *testing.T) {
	cs, err := Load(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err == nil || cs != nil {
		t.Fatalf("Load on a missing file = %v, %v; want nil and an error", cs, err)
	}
}

func TestCountsReportsTheShapeOfTheSetBeforeAnythingIsScored(t *testing.T) {
	cs := []Case{
		{ID: "a", Input: "x", Label: labelset.Retryable},
		{ID: "b", Input: "y", Label: labelset.NeedsHuman, Hard: true, Why: "policy call"},
		{ID: "c", Input: "z", Label: labelset.NeedsHuman},
		{ID: "d", Input: "w", Label: labelset.Retryable},
	}
	perLabel, hard := Counts(cs)
	if hard != 1 {
		t.Errorf("hard = %d, want 1", hard)
	}
	if perLabel[labelset.Retryable] != 2 || perLabel[labelset.NeedsHuman] != 2 {
		t.Errorf("perLabel = %v, want retryable=2 and needs_human=2", perLabel)
	}
	// A label the set does not contain reads as zero rather than as an absence, which is what
	// lets the run header print a row per label and still be right about an empty set.
	if perLabel[labelset.Expected] != 0 {
		t.Errorf("perLabel[expected] = %d, want 0", perLabel[labelset.Expected])
	}
}

func TestCountsOnAnEmptySetIsZeroRatherThanAPanic(t *testing.T) {
	perLabel, hard := Counts(nil)
	if hard != 0 || len(perLabel) != 0 {
		t.Errorf("Counts(nil) = %v, %d; want an empty map and 0", perLabel, hard)
	}
}
