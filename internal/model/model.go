// Package model talks to an OpenAI-compatible chat endpoint and turns its answer into one
// of three labels. Everything about this file is strict: the contract is one JSON object,
// a missing confidence is a failure rather than a default, and an answer outside the
// closed label set is refused. A service that accepts loose answers cannot be evaluated,
// and its evaluation would be measuring the parser.
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/labelset"
)

// ErrUnparseable is returned when the endpoint answered but not with a decision. It is
// kept separate from a transport failure because the two mean different things: one is a
// provider that needs re-prompting, the other is a provider that is down.
var ErrUnparseable = errors.New("model output was not a usable decision")

// HTTPError carries the status, so the caller can tell a 503 (wait) from a 401 (nobody is
// going to fix this by retrying).
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return fmt.Sprintf("model endpoint returned HTTP %d: %s", e.Status, body)
}

const SystemPrompt = `You classify one line from a production log into exactly one of three labels.

retryable   - a transient infrastructure condition; retrying later is the right action
expected    - normal operation that happens to look like an error; nothing to do
needs_human - it will not fix itself; a person has to read it

Answer with one JSON object and nothing else:
{"label":"retryable|expected|needs_human","confidence":0.0}`

const ExplainPrompt = `In one sentence, say why this log line is classified as %s. Do not restate the label definition.`

type Config struct {
	BaseURL     string // e.g. http://localhost:11434/v1
	Model       string
	APIKey      string
	Timeout     time.Duration
	MaxTokens   int     // the label answer is tiny; the cap is what keeps it that way
	Temperature float64 // 0 in every configuration this project ships
	PriceIn     float64 // $ per million input tokens, for the cost table
	PriceOut    float64 // $ per million output tokens
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("model: BaseURL is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("model: Model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 64
	}
	return &Provider{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}, nil
}

func (p *Provider) Name() string { return "model" }

func (p *Provider) Prices() (in, out float64) { return p.cfg.PriceIn, p.cfg.PriceOut }

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *Provider) Predict(ctx context.Context, input string) (decision.Prediction, error) {
	start := time.Now()
	body, usage, err := p.chat(ctx, SystemPrompt, input, p.cfg.MaxTokens)
	if err != nil {
		return decision.Prediction{}, err
	}
	pred, err := ParseResponse([]byte(body))
	if err != nil {
		return decision.Prediction{}, err
	}
	pred.LatencyMS = decision.Elapsed(start)
	pred.TokensIn, pred.TokensOut = usage.in, usage.out
	return pred, nil
}

// Explain asks for one sentence. It is a second call on purpose: mixing prose into the
// labelling call is how a label quietly becomes a paragraph, and it also means the label
// is still produced when the explanation fails.
func (p *Provider) Explain(ctx context.Context, input string, label labelset.Label) (string, error) {
	body, _, err := p.chat(ctx, fmt.Sprintf(ExplainPrompt, label), input, 120)
	if err != nil {
		return "", err
	}
	var parsed chatResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	if len(parsed.Choices) == 0 {
		return "", ErrUnparseable
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

type usage struct{ in, out int }

func (p *Provider) chat(ctx context.Context, system, user string, maxTokens int) (string, usage, error) {
	payload := chatRequest{
		Model:       p.cfg.Model,
		Temperature: p.cfg.Temperature,
		MaxTokens:   maxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", usage{}, err
	}
	url := strings.TrimSuffix(p.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", usage{}, err
	}
	defer resp.Body.Close()
	// A bounded read: an endpoint that answers with a megabyte of prose must not be able
	// to decide how much memory this service uses.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", usage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", usage{}, &HTTPError{Status: resp.StatusCode, Body: string(body)}
	}
	content, in, out, err := ParseChatResponse(body)
	if err != nil {
		return "", usage{}, err
	}
	return content, usage{in: in, out: out}, nil
}

// ParseChatResponse reads the envelope and returns the assistant's text with the token
// counts. It is exported, and the recorded provider calls it, because a fixture parsed by
// a different reader would prove something about that reader instead of about production.
func ParseChatResponse(body []byte) (content string, tokensIn, tokensOut int, err error) {
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, 0, fmt.Errorf("%w: envelope is not JSON: %v", ErrUnparseable, err)
	}
	if parsed.Error != nil {
		return "", 0, 0, fmt.Errorf("model endpoint reported an error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", 0, 0, fmt.Errorf("%w: no choices in the response", ErrUnparseable)
	}
	return parsed.Choices[0].Message.Content, parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens, nil
}

// ParseResponse is exported because the recorded provider and the tests must go through
// exactly this code: a fixture that is parsed by a different parser would prove nothing
// about the parser that runs in production.
func ParseResponse(raw []byte) (decision.Prediction, error) {
	text := strings.TrimSpace(string(raw))
	text = stripFences(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return decision.Prediction{}, fmt.Errorf("%w: no JSON object in %q", ErrUnparseable, clip(text))
	}

	var answer struct {
		Label      string   `json:"label"`
		Confidence *float64 `json:"confidence"`
	}
	dec := json.NewDecoder(strings.NewReader(text[start : end+1]))
	if err := dec.Decode(&answer); err != nil {
		return decision.Prediction{}, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	label, err := labelset.Parse(answer.Label)
	if err != nil || !labelset.IsPredictable(label) {
		return decision.Prediction{}, fmt.Errorf("%w: label %q is not one of the three", ErrUnparseable, answer.Label)
	}
	// A missing confidence is a failure rather than a default. The prompt asked for it,
	// and inventing 0.8 on the model's behalf would put a number into the threshold check
	// that no part of the system produced.
	if answer.Confidence == nil {
		return decision.Prediction{}, fmt.Errorf("%w: no confidence in the answer", ErrUnparseable)
	}
	conf := *answer.Confidence
	if conf < 0 || conf > 1 {
		return decision.Prediction{}, fmt.Errorf("%w: confidence %.2f is outside 0..1", ErrUnparseable, conf)
	}
	return decision.Prediction{Label: label, Confidence: conf, Source: "model"}, nil
}

// stripFences removes a markdown code fence, because enough models wrap their JSON in one
// that refusing it would measure the wrapper rather than the answer.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
