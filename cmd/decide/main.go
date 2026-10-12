// Command decide has two modes and no others: `server` answers one line at a time, and
// `eval` scores a provider over the frozen case set and compares the result with a
// committed baseline. The second mode is the one that makes the first one honest.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lasttoss/decision-model-service/internal/api"
	"github.com/lasttoss/decision-model-service/internal/cases"
	"github.com/lasttoss/decision-model-service/internal/decide"
	"github.com/lasttoss/decision-model-service/internal/decision"
	"github.com/lasttoss/decision-model-service/internal/evaluate"
	"github.com/lasttoss/decision-model-service/internal/labelset"
	"github.com/lasttoss/decision-model-service/internal/metrics"
	"github.com/lasttoss/decision-model-service/internal/model"
	"github.com/lasttoss/decision-model-service/internal/model/recorded"
	"github.com/lasttoss/decision-model-service/internal/rules"
	"github.com/lasttoss/decision-model-service/internal/score"
)

func main() {
	var (
		mode      = flag.String("mode", "server", "server | eval")
		addr      = flag.String("addr", envOr("DECIDE_ADDR", ":8080"), "listen address (server)")
		provider  = flag.String("provider", "rules", "rules | model | recorded")
		threshold = flag.Float64("threshold", 0.6, "confidence below which the service abstains")
		explain   = flag.Bool("explain", false, "ask the model for one sentence (costs output tokens)")

		modelURL     = flag.String("model-url", os.Getenv("DECIDE_MODEL_URL"), "OpenAI-compatible base URL, e.g. http://localhost:11434/v1")
		modelName    = flag.String("model-name", os.Getenv("DECIDE_MODEL_NAME"), "model name")
		modelKey     = flag.String("model-key", os.Getenv("DECIDE_MODEL_KEY"), "bearer token for the endpoint")
		recordedPath = flag.String("recorded", "testdata/model-envelopes.synthetic.jsonl", "recorded responses (provider=recorded)")
		modelTimeout = flag.Duration("model-timeout", 20*time.Second, "per-request timeout towards the model")

		casePath       = flag.String("cases", "eval/cases.jsonl", "the frozen case set (eval)")
		baselinePath   = flag.String("baseline", "eval/baseline-rules.json", "baseline to compare against (eval)")
		updateBaseline = flag.Bool("update-baseline", false, "write the baseline instead of comparing (eval)")
		tolerance      = flag.Float64("tolerance", evaluate.DefaultTolerance, "how much a metric may drop before the gate fails (eval)")
		jsonOut        = flag.String("json", "", "write the report to this path (eval)")
		mistakes       = flag.Int("mistakes", 0, "print this many wrong cases, with the reason for hard ones (eval)")
		priceIn        = flag.Float64("price-in", 3.0, "model price $/million input tokens (eval)")
		priceOut       = flag.Float64("price-out", 15.0, "model price $/million output tokens (eval)")
	)
	flag.Parse()

	if *mode != "server" && *mode != "eval" {
		fmt.Fprintf(os.Stderr, "unknown mode %q: want server or eval\n", *mode)
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch *mode {
	case "eval":
		os.Exit(runEval(ctx, evalConfig{
			provider: *provider, cases: *casePath, baseline: *baselinePath,
			update: *updateBaseline, tolerance: *tolerance, jsonOut: *jsonOut, mistakes: *mistakes,
			threshold: *threshold, explain: *explain, priceIn: *priceIn, priceOut: *priceOut,
			modelURL: *modelURL, modelName: *modelName, modelKey: *modelKey, recorded: *recordedPath,
			modelTimeout: *modelTimeout,
		}))

	case "server":
		chain, err := providerChain(*provider, *modelURL, *modelName, *modelKey, *recordedPath, *modelTimeout)
		if err != nil {
			log.Fatalf("providers: %v", err)
		}
		service := &decide.Service{Providers: chain, Threshold: *threshold, Explain: *explain, Counters: metrics.New()}
		srv := &api.Server{Service: service, Started: time.Now()}
		httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.Printf("decision-model-service listening on %s: providers=%s threshold=%.2f explain=%t",
				*addr, strings.Join(service.Names(), ","), service.Threshold, service.Explain)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("listen: %v", err)
			}
		}()
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}
}

type evalConfig struct {
	provider, cases, baseline               string
	update                                  bool
	tolerance                               float64
	jsonOut                                 string
	mistakes                                int
	threshold                               float64
	explain                                 bool
	priceIn, priceOut                       float64
	modelURL, modelName, modelKey, recorded string
	modelTimeout                            time.Duration
}

func runEval(ctx context.Context, cfg evalConfig) int {
	cs, err := cases.Load(cfg.cases)
	if err != nil {
		log.Fatalf("cases: %v", err)
	}
	chain, err := providerChain(cfg.provider, cfg.modelURL, cfg.modelName, cfg.modelKey, cfg.recorded, cfg.modelTimeout)
	if err != nil {
		log.Fatalf("providers: %v", err)
	}
	if len(chain) != 1 {
		log.Fatalf("eval takes exactly one provider, got %d", len(chain))
	}
	// Prices only affect the cost line, which is why a run with the rule baseline reports
	// zero: rules spend no tokens, and a cost table that invented a price for them would be
	// worse than one that says zero.
	in, out := cfg.priceIn, cfg.priceOut
	if cfg.provider == "rules" {
		in, out = 0, 0
	}

	report, samples := evaluate.Run(ctx, chain[0], cs, evaluate.Options{
		Threshold: cfg.threshold, Explain: cfg.explain, PriceIn: in, PriceOut: out,
	})

	perLabel, hard := cases.Counts(cs)
	fmt.Print(renderRunHeader(cfg, len(cs), hard, perLabel))
	fmt.Print(renderReport(report))
	if cfg.mistakes > 0 {
		fmt.Print(renderMistakes(cs, samples, cfg.mistakes))
	}

	if cfg.jsonOut != "" {
		if err := evaluate.SaveReport(cfg.jsonOut, report); err != nil {
			log.Fatalf("write report: %v", err)
		}
		fmt.Printf("report            %s\n", cfg.jsonOut)
	}

	if cfg.update {
		if err := evaluate.SaveReport(cfg.baseline, report); err != nil {
			log.Fatalf("write baseline: %v", err)
		}
		fmt.Printf("gate              baseline written to %s\n", cfg.baseline)
		return 0
	}

	base, err := evaluate.LoadReport(cfg.baseline)
	if err != nil {
		fmt.Printf("gate              %s\n", cfg.baseline)
		fmt.Printf("                  FAIL no baseline to compare against: %v\n", err)
		fmt.Println("                  run `make baseline` to create one, deliberately")
		return 1
	}
	regressions := evaluate.Compare(base, report, cfg.tolerance)
	fmt.Printf("gate              %s (tolerance %.3f)\n", cfg.baseline, cfg.tolerance)
	if len(regressions) == 0 {
		fmt.Println("                  PASS no metric dropped")
		return 0
	}
	for _, reg := range regressions {
		fmt.Printf("                  FAIL %s %.4f -> %.4f\n", reg.Metric, reg.Baseline, reg.Current)
		if reg.Note != "" {
			fmt.Printf("                       %s\n", reg.Note)
		}
	}
	return 1
}

func renderRunHeader(cfg evalConfig, n, hard int, perLabel map[labelset.Label]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider          %s\n", cfg.provider)
	fmt.Fprintf(&b, "threshold         %.2f (below this the service abstains)\n", cfg.threshold)
	fmt.Fprintf(&b, "cases             %d, of which %d are hard\n", n, hard)
	fmt.Fprintf(&b, "case labels       ")
	for _, l := range labelset.All() {
		fmt.Fprintf(&b, "%s=%d ", l, perLabel[l])
	}
	fmt.Fprintf(&b, "\n\n")
	return b.String()
}

// renderReport prints the numbers this repository is willing to defend, in an order that
// puts the honest limit next to the headline: coverage and the hard score are not
// footnotes.
func renderReport(r score.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s %s\n", "metric", "value")
	fmt.Fprintf(&b, "%-16s %.4f\n", "macro_f1", r.MacroF1)
	fmt.Fprintf(&b, "%-16s %.4f  (over the %d it committed to)\n", "accuracy", r.AccuracyWhenCommitted, r.Committed)
	fmt.Fprintf(&b, "%-16s %.4f  (%d of %d abstained)\n", "coverage", r.Coverage, r.Abstained, r.N)
	if r.Hard > 0 {
		fmt.Fprintf(&b, "%-16s %.4f  (%d of %d)\n", "hard_accuracy", float64(r.HardCorrect)/float64(r.Hard), r.HardCorrect, r.Hard)
	}
	fmt.Fprintf(&b, "%-16s %.4f $/1k decisions (price %.2f/%.2f per Mtok)\n", "cost", r.CostPer1kDecisions, r.PricePerMTokIn, r.PricePerMTokOut)
	fmt.Fprintf(&b, "%-16s p50 %.2fms  p95 %.2fms\n", "latency", r.LatencyP50MS, r.LatencyP95MS)
	if len(r.Degraded) == 0 {
		fmt.Fprintf(&b, "%-16s none\n", "degraded")
	} else {
		for reason, count := range r.Degraded {
			fmt.Fprintf(&b, "%-16s %s=%d\n", "degraded", reason, count)
		}
	}
	fmt.Fprintf(&b, "\n%-16s %-6s %-9s %-9s %-9s %s\n", "per label", "sup", "pred", "prec", "recall", "f1")
	for _, l := range labelset.All() {
		ls := r.PerLabel[l]
		if ls.Support == 0 && ls.Predicted == 0 {
			continue
		}
		fmt.Fprintf(&b, "%-16s %-6d %-9d %-9.3f %-9.3f %.3f\n", l, ls.Support, ls.Predicted, ls.Precision, ls.Recall, ls.F1)
	}
	fmt.Fprintf(&b, "\n%-16s want -> got\n", "confusion")
	for _, want := range labelset.All() {
		row := r.Confusion[want]
		if len(row) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %-14s", want)
		keys := make([]string, 0, len(row))
		for got := range row {
			keys = append(keys, got)
		}
		sortStrings(keys)
		for _, got := range keys {
			fmt.Fprintf(&b, " %s:%d", got, row[got])
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b)
	return b.String()
}

// renderMistakes prints individual failures. For a hard case the `why` from the case file
// is printed too, because "the rules got 13 of these wrong" is only useful next to "and
// here is what they misread".
func renderMistakes(cs []cases.Case, samples []score.Sample, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s (up to %d)\n", "mistakes", limit)
	shown := 0
	for i, s := range samples {
		if s.Outcome.Abstained || s.Outcome.Label == s.Want {
			continue
		}
		if shown >= limit {
			break
		}
		c := cs[i]
		mark := ""
		if c.Hard {
			mark = " [hard]"
		}
		fmt.Fprintf(&b, "  %s want=%s got=%s conf=%.2f%s\n", c.ID, c.Label, s.Outcome.Label, s.Outcome.Confidence, mark)
		fmt.Fprintf(&b, "    input: %s\n", clip(c.Input, 96))
		if c.Why != "" {
			fmt.Fprintf(&b, "    why:   %s\n", clip(c.Why, 140))
		}
		shown++
	}
	if shown == 0 {
		fmt.Fprintf(&b, "  none\n")
	}
	fmt.Fprintln(&b)
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// providerChain builds the order the service tries. Rules are always last because they
// always answer: a chain without a fallback would be a service that goes quiet when its
// model does.
func providerChain(name, url, modelName, key, recordedPath string, timeout time.Duration) ([]decision.Provider, error) {
	switch name {
	case "rules":
		return []decision.Provider{rules.New()}, nil
	case "recorded":
		p, err := recorded.Load(recordedPath)
		if err != nil {
			return nil, err
		}
		return []decision.Provider{p}, nil
	case "model":
		p, err := newModel(url, modelName, key, timeout)
		if err != nil {
			return nil, err
		}
		// Model first, rules as the fallback: the only configuration in which a
		// degradation means something.
		return []decision.Provider{p, rules.New()}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q: want rules, model or recorded", name)
	}
}

func newModel(url, name, key string, timeout time.Duration) (*model.Provider, error) {
	return model.New(model.Config{BaseURL: url, Model: name, APIKey: key, Timeout: timeout, Temperature: 0, MaxTokens: 64})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
