// Package recorded answers from a file of recorded model responses. It exists so the
// model path can run in tests and in CI with no network and no key, and it is deliberately
// indistinguishable from the real provider: same Name, same parser, same refusals. A
// fixture answered by a kinder parser would prove nothing about the one in production.
package recorded

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/model"
)

type entry struct {
	Input    string `json:"input"`
	Response string `json:"response"`
}

type Provider struct {
	byInput map[string]string
}

func Load(path string) (*Provider, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p := &Provider{byInput: map[string]string{}}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var e entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if e.Input == "" {
			return nil, fmt.Errorf("%s:%d: input is empty", path, line)
		}
		if _, dup := p.byInput[e.Input]; dup {
			return nil, fmt.Errorf("%s:%d: input recorded twice", path, line)
		}
		p.byInput[e.Input] = e.Response
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(p.byInput) == 0 {
		return nil, fmt.Errorf("%s: no recorded responses", path)
	}
	return p, nil
}

func (p *Provider) Name() string { return "model" }

func (p *Provider) Predict(_ context.Context, input string) (decision.Prediction, error) {
	raw, ok := p.byInput[input]
	if !ok {
		// Refusing is the point. A recorded provider that invented an answer for an
		// unrecorded input would let a fixture file quietly become the case set.
		return decision.Prediction{}, fmt.Errorf("recorded: no response for input %q", clip(input))
	}
	// The envelope is read by the same function the real provider uses, so a recording
	// exercises the production path and not a shortcut written for tests.
	content, tokensIn, tokensOut, err := model.ParseChatResponse([]byte(raw))
	if err != nil {
		return decision.Prediction{}, err
	}
	pred, err := model.ParseResponse([]byte(content))
	if err != nil {
		return decision.Prediction{}, err
	}
	pred.TokensIn, pred.TokensOut = tokensIn, tokensOut
	return pred, nil
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
