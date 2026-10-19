# decision-model-service

[![ci](https://github.com/lasttoss/decision-model-service/actions/workflows/ci.yml/badge.svg)](https://github.com/lasttoss/decision-model-service/actions/workflows/ci.yml)

A narrow decision service: one log line in, one of three labels out, with a confidence, a
fallback that always answers, and an evaluation that decides whether any of it is worth
keeping. Go, standard library only, no database, and a case set written by hand.

Everything quoted in this file was printed by the commands shown, on a machine with no model
reachable. Where a number is missing, this file says why instead of filling it in.

```
make demo        # three stories: the measurement, the fallback, the model path
make test        # go test -race ./...  (no network, no database, no key)
make eval        # score the rule baseline and compare with the committed baseline - the gate
make baseline    # rewrite the baseline, deliberately, after reading the diff
make baseline-model  # write the model baseline from a real endpoint, once
make eval-model      # score a real model against that baseline
make readme-check    # every command named in this file resolves, and the offline ones are run
```

## What this demonstrates

| capability | how it is done here | where |
|---|---|---|
| **a closed answer space** | three labels, parsed strictly; a label outside the set, prose instead of an object, or a missing confidence is refused rather than resolved | `internal/labelset`, `internal/model.ParseResponse` |
| **abstention as a first-class outcome** | below the threshold the service reports `abstained: true` and still reports its best guess; the evaluation counts abstentions and reports coverage rather than scoring them as wrong | `internal/decide`, `internal/score` |
| **a fallback that is visible, not silent** | providers are tried in order; a fallback sets `degraded: true` with a stable reason (`provider_timeout`, `provider_unparseable`, …) that is also a metric label | `internal/decide.Decide`, `internal/metrics` |
| **an explanation that can never fail a decision** | explanations are a second call, only when asked, attributed with `explain_by_model`, and counted when they fail | `internal/decide`, there is a test for it |
| **a gate that has been seen to fail** | the evaluation compares against a committed baseline and exits non-zero on a drop; a unit test and a CI step both doctor the baseline and assert the failure | `internal/evaluate.Compare`, `.github/workflows/ci.yml` |
| **cost and latency next to quality** | one forward pass, no output tokens on the default path, and a report that carries the price it used so a cost figure cannot be read without its assumption | `internal/score`, `cmd/decide` |

## The numbers, as printed

The case set is 128 hand-written inputs, thirteen of which are marked *hard* - the obvious
keyword reading disagrees with the label. The provider here is the **rule baseline**, which
is the one provider that always answers.

```
$ make eval
provider          rules
threshold         0.60 (below this the service abstains)
cases             128, of which 13 are hard
case labels       retryable=38 needs_human=46 expected=44

metric           value
macro_f1         0.5454
accuracy         0.9423  (over the 52 it committed to)
coverage         0.4062  (76 of 128 abstained)
hard_accuracy    0.2308  (3 of 13)
cost             0.0000 $/1k decisions (price 0.00/0.00 per Mtok)
latency          p50 0.04ms  p95 0.05ms
degraded         none

per label        sup    pred      prec      recall    f1
retryable        38     19        0.895     0.447     0.596
needs_human      46     18        0.944     0.370     0.531
expected         44     15        1.000     0.341     0.508

confusion        want -> got
  retryable      abstain:21 retryable:17
  needs_human    abstain:28 needs_human:17 retryable:1
  expected       abstain:27 expected:15 needs_human:1 retryable:1

gate              eval/baseline-rules.json (tolerance 0.010)
                  PASS no metric dropped
```

Read that honestly and it says: **the keyword baseline is precise and timid.** When it
commits it is right 94% of the time, and it commits 41% of the time - on the rest it
abstains, because two patterns pointing at different labels leave its evidence-share below
the threshold. On the hard subset it is nearly useless: 3 of 13.

## The threshold is a policy, and the trade is measurable

Same provider, same cases, threshold lowered from 0.6 to 0.4:

```
metric           value
macro_f1         0.5501
accuracy         0.9259  (over the 54 it committed to)
coverage         0.4219  (74 of 128 abstained)
hard_accuracy    0.3077  (4 of 13)
```

Coverage up, accuracy down, hard cases slightly better. The gate **fails** this run when it is
compared with the committed baseline, and that is the gate working: a policy change that moves
quality is supposed to require a deliberate `make baseline`. Two of the three compared metrics
are in tension on purpose - a single number would have hidden exactly this.

## What the model column says

A rule table and a model, the same 128 cases and the same threshold, scored by the two commands:

| | rules (`make eval`) | gemma4:31b-cloud (`make eval-model`) |
|---|---|---|
| macro F1 | 0.5454 | **0.9446** |
| accuracy | 0.3828 | 0.9453 |
| accuracy over what it committed to | 0.9423 | 0.9453 |
| coverage | 0.406 (76 of 128 abstained) | **1.000** |
| hard cases (of 13) | 3 | **12** |
| latency p50 / p95 | 0.04 / 0.05 ms | 509 / 874 ms |
| cost per 1k decisions | $0 (no tokens) | $0.5987 |
| degraded | none | none |

Read the third line before the first. **The rule table is as accurate as the model on the cases it is
willing to answer** - 0.9423 against 0.9453 - and it answers 52 of 128. So the model's advantage is
not being right more often; it is refusing less often, and that is what the 509 milliseconds and the
60 cents per thousand decisions buy. The repository is built so that this distinction is visible
rather than averaged away: coverage, `accuracy_when_committed` and the abstention count are in the
report and in the baseline file, not just the headline F1.

The model was wrong on five of the 128, and the report prints the five with its own reasoning:

```
c022 want=needs_human got=expected conf=0.90 [hard]
  input: failed to acquire lease on shard 3: already held by another leader
  why:   this looks transient, but two leaders on one shard is a split-brain symptom, not load
c029 want=retryable got=expected conf=1.00
  input: the replica is catching up, reads will be served shortly
```

Two runs stand behind that table, and the first one is worth keeping. Pointed at an endpoint whose
model had been retired that month, the harness returned `coverage 0.0000 · macro_f1 0.0000 ·
degraded provider_client_error=128`: 128 abstentions, a reason on every answer, and nothing scored as
right. A dead endpoint produced a report that said a dead endpoint was the problem, which is the whole
point of the abstention column - the second run is the live one.

```
$ make baseline-model MODEL_NET='--network host' \
    DECIDE_MODEL_URL=http://localhost:11434/v1 DECIDE_MODEL_NAME=<a model>
$ make eval-model MODEL_NET='--network host' \
    DECIDE_MODEL_URL=http://localhost:11434/v1 DECIDE_MODEL_NAME=<a model>
```

The first writes `eval/baseline-model.json`, including the cost line computed from the price you pass
in (`PRICE_IN`/`PRICE_OUT`, dollars per million tokens, defaulting to 3 and 15 - a flag, not a
lookup, so the cost line is only as true as the price you give it). The second scores the same
endpoint against that snapshot and fails on a drop of more than `TOLERANCE`.

Re-run against the baseline it wrote, the same command reproduced every quality metric to four
decimals and moved only the latency and cost lines, which is the shape a snapshot comparison should
have. It is a snapshot of one endpoint on one day, not a CI gate: a model at temperature 0 still
answers slightly differently each time, and the file is meant to be rewritten with `make baseline-model` after
reading the diff. The scoring runs in a container, so the endpoint it is given is the container's view
of the network: `host.docker.internal` is mapped to the host for an endpoint listening on every
interface, and `MODEL_NET='--network host'` with a `localhost` URL for one bound to the host's
loopback, which is ollama's default.

The model path itself is exercised in CI with no network, through
`testdata/model-envelopes.synthetic.jsonl` - **hand-written envelopes, not recordings from a
model** - which is a test of the transport and the parser contract and is labelled as such in
the file, in `make eval-recorded`, and here.

## How it works

**Figure 1.** One line enters. A model is asked first, if there is one; a rule table always
answers and is always last, so a model failure becomes a visible degradation rather than
silence. The evaluation loop below is what keeps the claims above honest, and it runs on every
push and nightly in the cluster.

![decision-model-service flow: one log line goes to an optional model and then to a rule baseline that always answers; below the confidence threshold the service abstains while still reporting its best guess; a separate evaluation loop scores a provider over 128 frozen cases and compares the result with a committed baseline, failing CI and a nightly CronJob on a drop](docs/figures/decision-model-service-flow.png)

The figure is generated by a committed script that measures its own text before placing a box
(`docs/figures/decision-model-service-flow.generator.py`), and `make diagram` fails when the
export is older than the sources named in its `diagram-meta`.

## The HTTP contract

```
GET  /healthz              → {"status":"ok","providers":["rules"],"threshold":0.6,"explain":false}
POST /v1/decide            → the outcome, always 200
GET  /metrics              → the exposition format, hand-rendered
```

```json
{
  "label": "retryable",
  "confidence": 0.97,
  "source": "model",
  "detail": "",
  "latency_ms": 41.2,
  "tokens_in": 148,
  "tokens_out": 11,
  "abstained": false,
  "degraded": false,
  "total_ms": 44.8
}
```

Three decisions in that shape are worth naming:

1. **Always 200.** A degraded answer is an answer, and the caller can see `degraded: true`. A
   503 would make a log pipeline drop the line, which is strictly worse than being told
   "needs_human, low confidence".
2. **`source` is on the answer**, not only in a log line: a caller that cannot tell a rule from
   a model cannot tell what it is trusting.
3. **`total_ms` is the caller's wait**, fallback included, and it is the number the evaluation
   reports as p50/p95. A latency that excludes the time spent failing is a latency nobody
   experiences.

There is no authentication, and that is stated rather than implied: this is a helper a log
pipeline calls inside a cluster, the NetworkPolicy in the chart is on by default, and inventing
a token scheme for a demo would teach nothing about the part of this repository worth reading.

## What this does not claim

- **The case set is mine, and it is small.** 128 inputs, written by the same person who wrote
  the rules. A model that scores well on it is not thereby good at triage. What is demonstrated
  is the *measurement* - frozen cases, a baseline, a gate that fails, a cost model - and the
  limits of a set this size are part of the result.
- **No model quality number.** See above: the column is empty and the command that fills it is
  the deliverable. Filling it with a number from a different machine would be worse than empty.
- **Not RAG, not fine-tuning, not serving at scale.** One forward pass, temperature 0, a
  64-token cap, CPU-only in practice.
- **`exactly-once`-style guarantees do not apply here.** The service is stateless; there is
  nothing to reconcile.
- **The rules are not a product.** They exist to be beaten and to answer when the model cannot.

## Repository standard

| item | here | note |
|---|---|---|
| `docker-compose.yml` | ✅ | one API service; the evaluation is a one-off `docker run` so `compose up` cannot accidentally run the whole case set |
| `Dockerfile` | ✅ | two stages, distroless `static-debian12:nonroot`, and it carries `eval/` and `testdata/` so the evaluation runs inside the image with no network |
| Helm chart | ✅ | `charts/decision-model-service`: Deployment, Service, a **nightly evaluation CronJob**, NetworkPolicy on by default |
| `docs/figures/` + freshness gate | ✅ | `decision-model-service-flow` with `diagram-meta`; `make diagram` fails on a stale export; the `.png` export is checked against the `.html` it came from, and the checker is in `scripts/`, so CI runs it |
| tests + coverage in CI | ✅ | 9 packages, no network and no database needed; coverage printed on every run |
| documented commands | ✅ | `make readme-check` resolves every command this file names and runs the offline ones - including `make baseline-model` and `make eval-model`, against a stub endpoint rather than a model |
| README with what it demonstrates | ✅ | this file |

## Licence

MIT. See `LICENSE`. What was carried over and what was not is in `PROVENANCE.md`.
