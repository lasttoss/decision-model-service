# Design notes

What I chose, what I did not, and why - in the order the decisions matter.

## An abstention is not an answer

**Chosen:** below the threshold the service returns `abstained: true` **and** the label it would
have given, and the evaluation counts abstentions separately, reporting coverage next to
accuracy.

**Not chosen:** silently returning the low-confidence label, or returning an error. Both hide
the same thing from different directions. A service that guesses on everything is worse than
one that admits the gap, because a caller cannot tell the difference between a confident
answer and a coin flip - and the evaluation would score the guess either way.

The number that makes this real is coverage: 0.41 for the rule baseline at threshold 0.6. That
is the *cost* of abstaining, and it is on the first line of the report rather than in a
footnote.

## The threshold is a policy, not a default

**Chosen:** a required value, with the trade measured in the README at two settings.

**Not chosen:** a constant in the code. The right number is different in a tool that pages a
human (0.6, send the uncertain ones to a person) and in one that retries automatically (0.9,
because a wrong retry is cheap but a wrong "it will fix itself" is not).

The measured trade - coverage 0.41 → 0.42 while accuracy 0.94 → 0.93 and hard cases 0.23 →
0.31 - is why the gate compares three metrics and not one composite score. A single number
would make that move look like noise.

## Rules are the fallback *and* the baseline

**Chosen:** a weighted pattern table, always last in the chain, and the thing the model is
compared against.

**Not chosen:** a second model as the fallback. A fallback that can fail the way the primary
fails is not a fallback, and a baseline that is another model makes "did the new prompt help?"
unanswerable - you cannot tell a change in the model from a change in the thing it is measured
against.

The weights are not decoration either: `permission denied` outweighs `timeout`, so a line that
says both lands on the more specific reading. The case set contains that shape on purpose, and
the rules still lose 10 of the 13 hard cases - which is the point of having them.

## Strict parsing, and no invented confidence

**Chosen:** the model must answer one JSON object with `label` and `confidence`. A missing
confidence is `ErrUnparseable`, not a default; prose is refused; a label outside the three is
refused; a `confidence` of 1.5 is refused.

**Not chosen:** treating a missing confidence as 0.8. That number would then enter the
threshold check, and no part of the system would have produced it - the worst kind of value,
because it looks like a measurement.

Markdown fences are stripped and text around the object is tolerated, because enough models
wrap their JSON that refusing the wrapper would measure the wrapper rather than the answer.
That is the one leniency in the parser, and it is bounded: there still has to be exactly one
object with both fields inside.

## The evaluation is part of the system

**Chosen:** a `--mode eval` in the same binary, a committed baseline, a CI job on every push,
and a **nightly CronJob in the chart** that exits non-zero when a metric drops.

**Not chosen:** a notebook, or a script that prints numbers somebody reads. Quality drifts with
model versions, prompt edits and case sets, and the only version of this that survives contact
with a busy month is the one that fails something.

Three details in the comparison are deliberate:

1. **A different case count is refused, not compared.** The case set is the ruler; if it
   changed length, every metric below it is measuring something else. Reporting a percentage
   drop there would be worse than reporting nothing.
2. **Cost is compared in the opposite direction.** Every other metric regressing means it went
   down; cost regressing means it went up.
3. **The tolerance is 0.01, not 0.** Float sums over a few hundred cases move in the last bits,
   and a gate that fails on that teaches people to re-run it until it passes - which is worse
   than no gate at all.

And the gate has been seen to fail, twice: a unit test doctors each metric in turn, and the CI
job doctors the committed baseline and asserts the non-zero exit and the metric name. A gate
nobody has watched fail is a claim.

## Latency is the caller's wait

**Chosen:** `total_ms` on the outcome, including a failed model attempt before the fallback, and
the evaluation reports p50/p95 of that.

**Not chosen:** reporting the provider's own time. A 900ms failed request followed by a 1ms rule
answer did not take 1ms, and a latency budget built on the provider's self-report would be
built on the number the caller never experiences.

Percentiles are nearest-rank rather than interpolated: p95 is a value some request actually
experienced, and can be reproduced from the raw data.

## No database, no dependencies, no key

**Chosen:** standard library only, in-memory counters, configuration from flags and environment.

**Not chosen:** a Postgres for the decision log, a metrics client, an SDK for the model
endpoint. The interesting content of this repository is the measurement and the fallback, and
every dependency added to carry it would be a page of code a reviewer has to take on trust.

The one thing that would justify a database - an audit trail of decisions - is deliberately not
here: it is the subject of the other repository in this portfolio, and duplicating it would
make both weaker.

## The case set's honesty

**Chosen:** cases authored in `eval/author_cases.py`, labels written by the same hand that wrote
the rules, and a `why` on the thirteen hard ones.

**Not chosen:** generating cases from the rules (they would score 100% and measure nothing) or
scraping log lines from anywhere real (they are not mine to publish).

The limitation is stated in three places on purpose - this file, `eval/README.md`, and the
README's "what this does not claim" - because the most likely way to overstate this project is
to present a small hand-written set as if it were an evaluation of a model.
