# Measurements

The cross-repository rule is that a repository making a claim has to be able to re-make it. Here the
claims are about a classifier, so the run is an evaluation, not a feeling.

## Environment

| | |
|---|---|
| Machine | 12-core x86-64 (AMD64), Ubuntu 24.04, Docker 29.1.3 |
| Go | 1.27.2 |
| Date | 2026-10-13 (UTC) |

Every host port in this file is the one the repository publishes: bound to loopback and offset from
the default, so a machine already running a Postgres, a Redis or three other stacks does not have to
stop them first.


## p50 0.04 ms, right 94% of the time, commits 41% of the time

```
$ make baseline            # 128 cases, 13 of which are hard
provider          rules
threshold         0.60 (below this the service abstains)
cases             128, of which 13 are hard

metric           value
macro_f1         0.5454
accuracy         0.9423  (over the 52 it committed to)
coverage         0.4062  (76 of 128 abstained)
hard_accuracy    0.2308  (3 of 13)
latency          p50 0.04ms  p95 0.05ms
degraded         none

gate              baseline written to eval/baseline-rules.json
```

**Result.** All three README numbers reproduce exactly, including the two that are printed to the
last digit: `latency p50 0.04ms` and `accuracy 0.9423`. The abstention rate is **0.4062 = 41%**, and
**0.9423 = 94%** is accuracy *over the 52 it committed to* - which is the honest denominator: a
service that abstains on 59% of its input can have a high accuracy over what it answers.

`hard_accuracy 0.2308` is printed in the same block and is deliberately not in the README's summary
line: 3 of 13 is not a number to lead with, and the evaluation says so.

## The fallback always answers

```
$ make demo
== 2. the fallback: model first, pointed at nothing ==
  label=retryable source=rules degraded=True reason=provider_error total_ms=0.31
== 3. the model path, from recorded envelopes (no network, no key) ==
  context canceled                              -> expected/0.94    source=model  tokens=142/11 degraded=False
  panic: runtime error: nil pointer dereference -> needs_human/0.97 source=model  tokens=142/11 degraded=False
  the frobnicator wobbled twice                 -> needs_human/0.00 source=none   tokens=0/0    degraded=True
```

**Result.** With no provider configured the service still answers, marks itself `degraded=True`, and
records why. Until an endpoint is given the evaluation leaves the model column empty and says so,
rather than filling it with something that looks like a result.

## The model column, two runs

The first run of `make eval-model` on this machine did not get as far as a model: it died in the flag
parser, then on a chain length, and both are fixed in the commit that carries these numbers.
`cmd/decide` required `len(chain) == 1` to score anything, while the model provider is deliberately a
chain of two - the model with the rule table behind it - so the one path that reports a model's
quality could not be started by the command the README documents.

| | |
|---|---|
| Machine | 12-core x86-64 (AMD64), 31 GB, Linux Mint 22.3, Docker 29.1.3 |
| Go | 1.25.13, in the repository's own `golang:1.25` image |
| Date | 2026-10-19 (UTC) |
| Price used | `PRICE_IN`/`PRICE_OUT` at their defaults, 3 and 15 $/Mtok |

```
$ make baseline-model MODEL_NET='--network host' \
    DECIDE_MODEL_URL=http://localhost:11434/v1 DECIDE_MODEL_NAME=<a model>
```

**Run 1, endpoint retired.** The endpoint answered every request with an API error - the model had
been retired that September - and the evaluation reported it as data rather than as a failure:

```
  macro_f1         0.0000
  coverage         0.0000  (128 of 128 abstained)
  hard_accuracy    0.0000  (0 of 13)
  latency          p50 245.85ms  p95 287.97ms
  degraded         provider_client_error=128
```

Nothing was guessed, nothing was scored as right, and the reason appears on the answer. This is the
abstention path doing its job on a model endpoint instead of a unit test.

**Run 2, live endpoint: `gemma4:31b-cloud`.**

```
  metric           value
  macro_f1         0.9446
  accuracy         0.9453  (over the 128 it committed to)
  coverage         1.0000  (0 of 128 abstained)
  hard_accuracy    0.9231  (12 of 13)
  cost             0.5987 $/1k decisions (price 3.00/15.00 per Mtok)
  latency          p50 509.23ms  p95 873.84ms
  degraded         none
```

Against the rule baseline in the same file: `macro_f1 0.5454`, `accuracy 0.3828`, `coverage 0.4063`,
`hard_accuracy 0.2308`. The line that matters is `accuracy_when_committed`: **0.9423 for the rules
against 0.9453 for the model**. The rule table is not less accurate than the model when it answers -
it answers 52 of 128 instead of 128 of 128. The comparison in the README is written that way.

Five cases were wrong, and the report prints them with the model's own reasoning. `c022`
(`failed to acquire lease on shard 3: already held by another leader`, wanted `needs_human`, got
`expected`) is the honest kind: the model explains that it reads as transient when two leaders on one
shard is a split-brain symptom, which is an argument a reviewer can disagree with on the merits.

**Run 3, the same command again.** `make eval-model` scored the same endpoint against the baseline
run 2 wrote and passed:

```
  gate              eval/baseline-model.json (tolerance 0.010)
                    PASS no metric dropped
  exit 0
```

Every quality metric came back identical to four decimals - `macro_f1 0.9446`, `accuracy 0.9453`,
`coverage 1.0000`, `hard_accuracy 0.9231` - and only the lines nobody should read as constants moved:
`latency p50 508.23` against `509.23` ms, `cost 0.5939` against `0.5987` $/1k. The five wrong cases
were the same five. That is a small piece of evidence about the endpoint and a larger one about the
report: quality is stable across runs, and the numbers that are not stable are labelled as latency
and price rather than folded into the score.

No unit test locks the chain-length fix: the eval path reports through `log.Fatalf`, so what proves it
is the three runs above, not a test. Worth a follow-up that makes the path return an error instead.
