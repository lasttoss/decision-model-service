#!/usr/bin/env python3
"""Generate docs/figures/decision-model-service-flow.html.

Measured, not positioned by eye: every box is sized from its own text, so editing a line
below cannot overlap anything. Same generator shape as the one in liveops-console, for the
same reason - a hand-placed diagram goes wrong the first time a label grows.

    python3 docs/figures/decision-model-service-flow.generator.py
    python3 <skill>/scripts/self_check.py docs/figures/decision-model-service-flow.html
    make diagram
"""
import html
import pathlib

PAPER, INK, MUTED, ACCENT, LINE, TINT = "#f5f5f5", "#2d3142", "#4f5d75", "#b3541e", "#c9ccd1", "#fdf4ef"
FONT = {"sans": "'Geist', system-ui, sans-serif",
        "mono": "'Geist Mono', ui-monospace, monospace",
        "serif": "'Instrument Serif', serif"}
ADVANCE = {"mono": 0.605, "sans": 0.525, "sans-bold": 0.565}
PAD, GAP = 16, 120

def esc(s):
    return html.escape(str(s))

def width(s, size, family="sans", weight="400"):
    key = "mono" if family == "mono" else ("sans-bold" if weight == "600" else "sans")
    return len(s) * ADVANCE[key] * size

def text(x, y, s, size=12.5, weight="400", fill=INK, family="sans", anchor="start"):
    return (f'<text x="{x:.0f}" y="{y:.0f}" font-family="{FONT[family]}" font-size="{size}" '
            f'font-weight="{weight}" fill="{fill}" text-anchor="{anchor}">{esc(s)}</text>')

def rect(x, y, w, h, fill="#ffffff", stroke=LINE, rx=10):
    return f'<rect x="{x:.0f}" y="{y:.0f}" width="{w:.0f}" height="{h:.0f}" rx="{rx}" fill="{fill}" stroke="{stroke}"/>'

def arrow(x1, y1, x2, y2, label="", dashed=False):
    dash = ' stroke-dasharray="5 4"' if dashed else ""
    out = [f'<line x1="{x1:.0f}" y1="{y1:.0f}" x2="{x2:.0f}" y2="{y2:.0f}" stroke="{MUTED}" stroke-width="1.6" '
           f'marker-end="url(#arrowhead)"{dash}/>']
    if label:
        mx, my = (x1 + x2) / 2, (y1 + y2) / 2
        lw = width(label, 11, "mono") + 12
        out.append(rect(mx - lw / 2, my - 12, lw, 20, fill=PAPER, stroke="none", rx=5))
        out.append(text(mx, my + 2, label, size=11, fill=MUTED, family="mono", anchor="middle"))
    return "".join(out)

class Block:
    def __init__(self, title, subtitle, lines, accent=INK, tint=None):
        self.title, self.subtitle, self.lines, self.tint = title, subtitle, lines, tint
        widths = [width(title, 15, weight="600")]
        if subtitle:
            widths.append(width(subtitle, 11.5, "mono"))
        for line, mono in lines:
            widths.append(width(line, 11.5, "mono" if mono else "sans"))
        self.w = max(widths) + PAD * 2
        self.h = PAD * 2 + 22 + (16 if subtitle else 0) + len(lines) * 17

    def render(self, x, y):
        out = [rect(x, y, self.w, self.h, fill=self.tint or "#ffffff")]
        out.append(text(x + PAD, y + PAD + 12, self.title, size=15, weight="600"))
        yy = y + PAD + 34
        if self.subtitle:
            out.append(text(x + PAD, yy + 4, self.subtitle, size=11.5, fill=MUTED, family="mono"))
            yy += 16
        for line, mono in self.lines:
            out.append(text(x + PAD, yy + 9, line, size=11.5, family="mono" if mono else "sans"))
            yy += 17
        return "".join(out)

INPUT = Block("one log line", "the whole question", [
    ("dial tcp 10.4.2.9:5432: i/o timeout", True),
    ("timeout …: permission denied   ← the hard ones", True),
])
MODEL = Block("model, optional", "OpenAI-compatible, temperature 0", [
    ("POST /chat/completions  max_tokens 64", True),
    ('answer: {"label":…,"confidence":…}', True),
    ("prose, a missing number, or a label outside the set", True),
    ("  → refused, counted, never guessed", True),
])
RULES = Block("rules, always", "45 weighted patterns, no network", [
    ("deadline exceeded · 503 · quota exceeded", True),
    ("context canceled · permission denied", True),
    ("no pattern matched → needs_human at 0.30", True),
])
THRESHOLD = Block("threshold", "the service's own decision", [
    ("confidence < 0.6 → abstained: true", True),
    ("the label is still reported", True),
    ("abstaining is not an answer: it costs coverage", True),
])
OUTCOME = Block("outcome", "always 200, always attributed", [
    ("label · confidence · source", True),
    ("abstained · degraded + reason", True),
    ("total_ms  (the caller's wait, fallback included)", True),
    ("explain, only when asked, marked model-written", True),
])
EVAL = Block("the evaluation that makes the above honest", "one command, one gate", [
    ("eval/cases.jsonl   128 cases, 13 of them hard", True),
    ("→ score: macro_f1, accuracy, coverage, hard, cost, p50/p95", True),
    ("→ compare eval/baseline-rules.json, tolerance 0.01", True),
    ("→ a model separately: make baseline-model, then eval-model", True),
    ("→ CI job + a nightly CronJob, both exit non-zero on a drop", True),
    ("a different case count is refused, not compared", True),
])
PROMISES = [
    "an abstention is counted, never scored as right",
    "a fallback is visible on the answer, with its reason",
    "the gate has been seen to fail, in the test suite and in CI",
]

col1 = max(INPUT.w, MODEL.w)
col2 = max(RULES.w, THRESHOLD.w)
col3 = max(OUTCOME.w, EVAL.w)
MARGIN, TOP = 28, 74
x1 = MARGIN
x2 = x1 + col1 + GAP
x3 = x2 + col2 + GAP
total_w = x3 + col3 + MARGIN

y = TOP
in_y = y + 24
model_y = y
rules_y = model_y + MODEL.h + 46
eval_y = rules_y + RULES.h + 46
strip_y = eval_y + EVAL.h + 46
strip_h = 78
H = strip_y + strip_h + 46
out_y = rules_y + RULES.h + 46
threshold_y = rules_y

svg = [f'<svg viewBox="0 0 {total_w:.0f} {H:.0f}" role="img" aria-labelledby="fig-title fig-desc" xmlns="http://www.w3.org/2000/svg">']
svg.append('<title id="fig-title">decision-model-service: the request path, the fallback, and the loop that measures both</title>')
svg.append('<desc id="fig-desc">One log line enters. An optional model is asked first over an OpenAI-compatible chat '
           'endpoint with a strict JSON contract; an answer that is prose, missing a confidence, or outside the three '
           'labels is refused. The weighted rule table always answers and is always last, so a model failure becomes a '
           'visible degradation with a reason rather than silence. Below the threshold the service abstains and still '
           'reports its best guess. A separate evaluation loop scores a provider over a frozen case set of 128 cases, '
           'thirteen of them deliberately hard, and compares the result with a committed baseline; a drop of more than '
           'the tolerance fails a CI job and a nightly CronJob.</desc>')
svg.append(f'<defs><marker id="arrowhead" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" '
           f'orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="{MUTED}"/></marker></defs>')

svg.append(INPUT.render(x1, in_y))
svg.append(MODEL.render(x2, model_y))
svg.append(RULES.render(x2, rules_y))
svg.append(THRESHOLD.render(x3, threshold_y))
svg.append(OUTCOME.render(x3, out_y))
svg.append(EVAL.render(x1, eval_y))

svg.append(arrow(x1 + col1, in_y + 40, x2, model_y + 40, "model first"))
svg.append(arrow(x2 + col2, rules_y + 40, x3, threshold_y + 40, "the answer"))
# The model falls back to the rules; the rules' answer then goes to the threshold check.
# An arrow between two points a pixel apart renders as a stray arrowhead, which is what the
# first version of this file produced - hence the explicit coordinates below.
svg.append(arrow(x2 + MODEL.w / 2, model_y + MODEL.h, x2 + MODEL.w / 2, rules_y, "on failure", dashed=True))
svg.append(arrow(x3 + THRESHOLD.w / 2, threshold_y + THRESHOLD.h, x3 + THRESHOLD.w / 2, out_y, "then"))

svg.append(rect(MARGIN, strip_y, total_w - MARGIN * 2, strip_h))
svg.append(text(MARGIN + PAD, strip_y + 26, "what the loop measures, and the three claims it is here to keep honest",
                size=12.5, weight="600", fill=MUTED))
chip_w = (total_w - MARGIN * 2 - PAD * 2 - 24) / 3
for i, promise in enumerate(PROMISES):
    cx = MARGIN + PAD + i * (chip_w + 12)
    svg.append(rect(cx, strip_y + 38, chip_w, 28, fill=TINT, stroke="#e8cdbd", rx=6))
    svg.append(text(cx + 10, strip_y + 57, f"{i + 1}. {promise}", size=11.5))
svg.append(text(MARGIN, H - 16, "make eval   → the same numbers, against the committed baseline; make baseline rewrites it, deliberately; make readme-check resolves every command this README names, and runs the offline ones",
                size=11, fill=MUTED, family="mono"))
svg.append("</svg>")

meta = ('<!-- diagram-meta {"topic":"decision-model-service-flow","level":"runtime","type":"flowchart",'
        '"question":"What answers one log line, what happens when the model does not, and how is any of it measured?",'
        '"sources":["internal/decide/decide.go","internal/model/model.go","internal/rules/rules.go","internal/score/score.go",'
        '"internal/evaluate/evaluate.go","internal/api/api.go","cmd/decide/main.go","eval/cases.jsonl","eval/baseline-rules.json","eval/baseline-model.json","Makefile",'
        '"charts/decision-model-service/templates/cronjob.yaml"],"updated":"2026-10-19"} -->')

page = f"""{meta}
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>decision-model-service</title>
<link href="https://fonts.googleapis.com/css2?family=Instrument+Serif:ital@0;1&family=Geist:wght@400;500;600&family=Geist+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<style>
  *, *::before, *::after {{ box-sizing: border-box; margin: 0; padding: 0; }}
  :root {{
    --color-paper: {PAPER}; --color-ink: {INK}; --color-muted: {MUTED}; --color-accent: {ACCENT};
    --font-sans: 'Geist', system-ui, sans-serif; --font-serif: 'Instrument Serif', serif;
    --font-mono: 'Geist Mono', ui-monospace, monospace;
  }}
  body {{ font-family: var(--font-sans); background: var(--color-paper); color: var(--color-ink);
         min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 3rem 2rem; }}
  .frame {{ max-width: {total_w + 40:.0f}px; width: 100%; }}
  .eyebrow {{ font-family: var(--font-mono); font-size: 0.66rem; font-weight: 500; letter-spacing: 0.18em;
              text-transform: uppercase; color: var(--color-muted); margin-bottom: 0.5rem; }}
  h1 {{ font-family: var(--font-serif); font-size: clamp(1.5rem, 2.4vw + 0.75rem, 2rem); font-weight: 400;
        letter-spacing: -0.02em; line-height: 1.15; color: var(--color-ink); margin-bottom: 1.5rem; }}
  .diagram-container {{ width: 100%; overflow-x: auto; }}
  svg {{ width: 100%; max-width: {total_w:.0f}px; display: block; }}
  .caption {{ font-family: var(--font-mono); font-size: 0.7rem; color: var(--color-muted); margin-top: 1rem; }}
</style>
</head>
<body>
<div class="frame">
  <p class="eyebrow">decision-model-service · runtime + evaluation</p>
  <h1>One line in, one label out, and a loop that keeps the answer honest</h1>
  <div class="diagram-container">
{"".join(svg)}
  </div>
  <p class="caption">Rules are last because they always answer: a chain without a fallback goes quiet when its model
  does, and a quiet triage tool is worse than a noisy one. The evaluation is not a notebook - it runs in CI on every
  push and in a nightly CronJob, and it has been seen to fail on purpose, in a test and in a job.</p>
</div>
</body>
</html>
"""
out = pathlib.Path(__file__).with_name("decision-model-service-flow.html")
out.write_text(page, encoding="utf-8")
print(f"wrote {out.name}: {total_w:.0f}x{H:.0f} viewBox, {len(page)} bytes")
