package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lasttoss/decision-model-service/internal/labelset"
)

func TestParseResponseStrictness(t *testing.T) {
	good := []struct {
		raw   string
		label labelset.Label
		conf  float64
	}{
		{`{"label":"retryable","confidence":0.9}`, labelset.Retryable, 0.9},
		{"```json\n{\"label\": \"needs_human\", \"confidence\": 0.7}\n```", labelset.NeedsHuman, 0.7},
		{`Here is my answer: {"label":"expected","confidence":1.0} — hope that helps`, labelset.Expected, 1.0},
	}
	for _, c := range good {
		pred, err := ParseResponse([]byte(c.raw))
		if err != nil {
			t.Errorf("ParseResponse(%q) failed: %v", c.raw, err)
			continue
		}
		if pred.Label != c.label || pred.Confidence != c.conf {
			t.Errorf("ParseResponse(%q) = %s/%.2f, want %s/%.2f", c.raw, pred.Label, pred.Confidence, c.label, c.conf)
		}
	}

	// Every one of these is a refusal, and each is here for a reason: prose instead of an
	// object, a label outside the closed set, a confidence that was asked for and not
	// given, and a number that is not a probability.
	bad := []string{
		"I think this is retryable",
		`{"label":"maybe","confidence":0.5}`,
		`{"label":"retryable"}`,
		`{"label":"retryable","confidence":1.5}`,
		`{"label":"abstain","confidence":0.5}`,
		``,
	}
	for _, raw := range bad {
		if _, err := ParseResponse([]byte(raw)); err == nil {
			t.Errorf("ParseResponse(%q) should have failed", raw)
		} else if !errors.Is(err, ErrUnparseable) {
			t.Errorf("ParseResponse(%q) should wrap ErrUnparseable, got %v", raw, err)
		}
	}
}

func TestPredictSendsAStrictRequestAndReadsTheUsage(t *testing.T) {
	var got chatRequest
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request was not JSON: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"label\":\"needs_human\",\"confidence\":0.83}"}}],"usage":{"prompt_tokens":120,"completion_tokens":9}}`))
	}))
	defer srv.Close()

	p, err := New(Config{BaseURL: srv.URL + "/v1", Model: "test-model", APIKey: "sk-test", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	pred, err := p.Predict(context.Background(), "panic: nil pointer dereference")
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if pred.Label != labelset.NeedsHuman || pred.Source != "model" {
		t.Fatalf("wrong prediction: %+v", pred)
	}
	if pred.TokensIn != 120 || pred.TokensOut != 9 {
		t.Fatalf("usage must be carried into the answer: %+v", pred)
	}
	if pred.LatencyMS <= 0 {
		t.Fatal("latency must be measured per prediction")
	}
	if got.Model != "test-model" || got.Temperature != 0 || got.Stream {
		t.Fatalf("the request must be deterministic and non-streaming: %+v", got)
	}
	if auth != "Bearer sk-test" {
		t.Fatalf("wrong auth header %q", auth)
	}
	// The prompt is part of the contract: a model that is not told the closed label set
	// will invent one, and the parser will refuse it for a reason that looks like the
	// model's fault.
	for _, label := range labelset.All() {
		if !strings.Contains(SystemPrompt, string(label)) {
			t.Errorf("the system prompt must name %s", label)
		}
	}
}

func TestTransportFailuresAreToldApart(t *testing.T) {
	t.Run("HTTP status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"model is loading"}}`))
		}))
		defer srv.Close()
		p, _ := New(Config{BaseURL: srv.URL, Model: "m"})
		_, err := p.Predict(context.Background(), "x")
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != 503 {
			t.Fatalf("want an HTTPError with 503, got %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}))
		defer srv.Close()
		p, _ := New(Config{BaseURL: srv.URL, Model: "m", Timeout: 50 * time.Millisecond})
		_, err := p.Predict(context.Background(), "x")
		if err == nil {
			t.Fatal("a slow endpoint must fail")
		}
		if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("a timeout should look like one, got %v", err)
		}
	})

	t.Run("empty choices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}))
		defer srv.Close()
		p, _ := New(Config{BaseURL: srv.URL, Model: "m"})
		if _, err := p.Predict(context.Background(), "x"); !errors.Is(err, ErrUnparseable) {
			t.Fatalf("an empty answer is unparseable, got %v", err)
		}
	})
}

func TestNewValidatesConfiguration(t *testing.T) {
	if _, err := New(Config{Model: "m"}); err == nil {
		t.Fatal("a missing BaseURL must be refused")
	}
	if _, err := New(Config{BaseURL: "http://x"}); err == nil {
		t.Fatal("a missing model name must be refused")
	}
	p, err := New(Config{BaseURL: "http://x", Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// Defaults are part of the contract: a caller that forgets a timeout gets one.
	if p.cfg.Timeout <= 0 || p.cfg.MaxTokens <= 0 {
		t.Fatalf("defaults must be applied: %+v", p.cfg)
	}
}
