# Provenance

A clean-room repository, written after hours on a personal machine, about a problem I keep
meeting at work: a model or a rule answers a narrow question, nobody can say how well it
answers it, and the first time anyone finds out is in production.

What was carried over is the *problem*, not the code:

| Carried over | Not carried over |
|---|---|
| The shape of the problem: one narrow question, a closed label set, a baseline, a threshold, a fallback | Any source file, prompt, schema, endpoint or configuration value from an employer's systems |
| The failure modes worth designing against: a model that answers prose instead of JSON, a service that goes quiet when its model does, a quality number that drifts because nobody compares it | Any log line, customer data, tenant name, host, bucket or credential from a live deployment |
| The lesson that a decision service is only as good as the way it is measured | Any model weights, fine-tune, or proprietary dataset |

Everything here was written for this repository: the labels, the rule table, the 128-case
set, the prompt, the parser, the metrics, the cost model and the chart.

Third-party software is used as a dependency (Go standard library only) or credited where it
inspired a design decision: the idea of a small, local, single-forward-pass decision service
came from reading the Apache-2.0 licensed `ollaya` project, which is why the design notes
argue for CPU-only, zero-output-token inference rather than for a hosted model.
