// Package cases is the frozen case set: the file the evaluation is allowed to be measured
// against, and the only thing in this repository that cannot be regenerated from the code.
package cases

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lasttoss/decision-model-service/internal/labelset"
)

// Case is one labelled input.
type Case struct {
	ID    string         `json:"id"`
	Input string         `json:"input"`
	Label labelset.Label `json:"label"`
	// Hard marks the inputs where the obvious keyword reading is wrong. They are the
	// reason the case set exists rather than being generated from the rules: a set built
	// from the rules would score them at 100% and measure nothing.
	Hard bool `json:"hard,omitempty"`
	// Why is required for a hard case. A surprising label with no reason is a mistake
	// waiting to be found by someone who cannot ask.
	Why string `json:"why,omitempty"`
}

func (c Case) validate() error {
	if c.ID == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(c.Input) == "" {
		return fmt.Errorf("%s: input is empty", c.ID)
	}
	if _, err := labelset.Parse(string(c.Label)); err != nil {
		return fmt.Errorf("%s: %w", c.ID, err)
	}
	if !labelset.IsPredictable(c.Label) {
		return fmt.Errorf("%s: abstain is not a label a case can have", c.ID)
	}
	if c.Hard && strings.TrimSpace(c.Why) == "" {
		return fmt.Errorf("%s: a hard case must say why the label is surprising", c.ID)
	}
	return nil
}

// Load reads JSONL. Blank lines and lines starting with # are ignored, so the case file
// can carry its own explanation where a reader will actually see it.
func Load(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Case
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var c Case
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s:%d: duplicate id %s", path, line, c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	return out, nil
}

// Counts reports the shape of a set, which the evaluation prints before it scores
// anything: a set that drifted to two labels would otherwise be discovered as a strange
// F1 rather than as a bad file.
func Counts(cs []Case) (perLabel map[labelset.Label]int, hard int) {
	perLabel = map[labelset.Label]int{}
	for _, c := range cs {
		perLabel[c.Label]++
		if c.Hard {
			hard++
		}
	}
	return perLabel, hard
}
