package recorded

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lasttoss/decision-model-service/internal/labelset"
	"github.com/lasttoss/decision-model-service/internal/model"
)

func load(t *testing.T) *Provider {
	t.Helper()
	p, err := Load(filepath.Join("..", "..", "..", "testdata", "model-envelopes.synthetic.jsonl"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return p
}

func TestRecordedAnswersGoThroughTheRealParser(t *testing.T) {
	p := load(t)
	pred, err := p.Predict(context.Background(), "context canceled")
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if pred.Label != labelset.Expected || pred.Source != "model" {
		t.Fatalf("wrong prediction: %+v", pred)
	}
	if pred.TokensIn != 142 {
		t.Fatalf("usage should be read from the recorded envelope, got %d", pred.TokensIn)
	}
}

// The fixture contains one response that is not a decision, so the refusal path is
// exercised by the same file the evaluation uses.
func TestUnparseableRecordingIsRefused(t *testing.T) {
	p := load(t)
	_, err := p.Predict(context.Background(), "the frobnicator wobbled twice")
	if !errors.Is(err, model.ErrUnparseable) {
		t.Fatalf("want ErrUnparseable, got %v", err)
	}
}

func TestUnrecordedInputIsRefused(t *testing.T) {
	p := load(t)
	if _, err := p.Predict(context.Background(), "something nobody recorded"); err == nil {
		t.Fatal("an unrecorded input must fail rather than be invented")
	}
}

func TestNameIsTheSameAsTheRealProvider(t *testing.T) {
	// If this drifts, an evaluation run through recordings would report a different source
	// than production and the comparison would be meaningless.
	if got := load(t).Name(); got != "model" {
		t.Fatalf("recorded provider must report itself as model, got %q", got)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty.jsonl":     "# only a comment\n",
		"bad.jsonl":       "{not json}\n",
		"noinput.jsonl":   "{\"response\":\"x\"}\n",
		"duplicate.jsonl": "{\"input\":\"a\",\"response\":\"{}\"}\n{\"input\":\"a\",\"response\":\"{}\"}\n",
	} {
		path := filepath.Join(dir, name)
		if err := writeFile(path, body); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s should have been rejected", name)
		}
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
