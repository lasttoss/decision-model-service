# GitHub metadata (paste into the repository settings)

**Description**
> A decision service that answers one line at a time with a label and a confidence, abstains when the confidence is low, falls back to rules when the model fails, and is measured against 200 hand-written cases by a CI gate that fails on a drop.

**Topics**
go, llm, classification, evaluation, model-serving, confidence, abstention, openai-compatible, cost-tracking, regression-gate, prometheus, ci

**Pinned blurb for the profile README**
> `decision-model-service` - a model-backed decision service whose honest limits (abstention rate, hard cases, $/1k decisions) sit next to the headline number, with a gate that has been seen to fail.
