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
records why. The model column stays empty until an endpoint is given, and the evaluation says that
out loud rather than filling it with something that looks like a result.
