# decision-model-service chart

A Deployment, a Service, a scheduled evaluation, and a NetworkPolicy that is on by default.

| value | default | why |
|---|---|---|
| `threshold` | `0.6` | a policy decision, not a tuning knob: below it the service abstains instead of answering |
| `model.url` | empty | with no URL the chain is the rule baseline alone and the service never degrades - a deployment can run this way forever |
| `model.timeout` | `20s` | short on purpose: the fallback exists so that a slow model costs a fallback, not a timeout on the caller |
| `model.priceIn/priceOut` | `0` | for the cost line only, left at zero rather than guessed |
| `explain` | `false` | the only part of a request that costs output tokens |
| `eval.enabled` | `true` | a nightly run that exits non-zero when a metric drops: quality drifts, and a failed Job is how someone finds out |
| `eval.baseline` | `eval/baseline-rules.json` | committed inside the image, so the comparison needs no network |
| `replicaCount` | `2` | stateless, and a rolling update should be invisible |
| `autoscaling.enabled` | `false` | rule decisions take microseconds; turn this on for a model deployment, with a measurement in hand |
| `networkPolicy.enabled` | `true` | `/healthz` and `/metrics` answer without a token, so the network is the access decision |

**No PodDisruptionBudget.** Two replicas behind a Service and a caller that retries is the
whole availability story; a PDB here would mostly get in the way of node drains.

**The CronJob is the interesting one.** It runs the same binary in `--mode eval`, inside the
cluster, against the committed baseline. That is what makes the evaluation part of the system
rather than a script someone ran once: a quality regression becomes a failed Job.
