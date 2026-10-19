#!/usr/bin/env bash
# Run the two commands that score a real model, against a stub instead of a model.
#
# `make baseline-model` and `make eval-model` are the commands the README tells people to run, and every
# part of them has been broken at least once: a flag that expanded to nothing, a chain length the eval
# path refused, a baseline file nothing wrote. None of it was visible in CI, which ran the rule baseline
# and nothing else.
#
# What this asserts, in order: that baseline-model *writes a file* (not just prints a line about one),
# that eval-model scores against that same file, and that the comparison passes.
set -euo pipefail

cd "$(dirname "$0")/.."
port=${STUB_PORT:-18999}
# The baseline has to be a path both sides agree on. The recipe runs in this repository's container,
# where the repository is /app and /tmp is the container's own; the guard that checks the file exists
# runs on the host. A relative path is the one thing that means the same to both, so the baseline is
# written at the repository root and removed again when this finishes.
baseline=.stub-baseline.json
rm -f "$baseline"

echo "  stub model on 127.0.0.1:$port"
go run ./testdata/stubmodel -addr "127.0.0.1:$port" >/tmp/stub-model.log 2>&1 &
stub=$!
trap 'kill $stub 2>/dev/null || true; rm -f "$baseline"' EXIT

for _ in $(seq 1 60); do
  if (exec 3<>/dev/tcp/127.0.0.1/"$port") 2>/dev/null; then break; fi
  sleep 0.5
done
kill -0 $stub 2>/dev/null || { echo "  the stub did not start:"; cat /tmp/stub-model.log; exit 1; }

# MODEL_NET puts the container on the host's network, which is how it reaches the stub on loopback.
run() { MODEL_NET='--network host' DECIDE_MODEL_URL="http://127.0.0.1:$port/v1" DECIDE_MODEL_NAME=stub "$@"; }

echo "  make baseline-model (against the stub)"
if ! run make baseline-model MODEL_BASE="$baseline" >/tmp/stub-baseline-run.txt 2>&1; then
  echo "  make baseline-model failed:"; tail -12 /tmp/stub-baseline-run.txt; exit 1
fi
[ -s "$baseline" ] || { echo "  no baseline at $baseline - the command printed a line and wrote nothing:"; tail -12 /tmp/stub-baseline-run.txt; exit 1; }
python3 - "$baseline" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
for key in ("macro_f1", "coverage", "n"):
    if key not in report:
        sys.exit("  the baseline has no " + key)
print("  the baseline has n=%s, macro_f1=%.4f, coverage=%.4f"
      % (report["n"], report["macro_f1"], report["coverage"]))
PY

echo "  make eval-model (against that baseline)"
if ! run make eval-model MODEL_BASE="$baseline" >/tmp/stub-compare-run.txt 2>&1; then
  echo "  make eval-model failed:"; tail -12 /tmp/stub-compare-run.txt; exit 1
fi
# The output goes to a file and is grepped afterwards: `grep -q` in a pipeline exits as soon as it
# matches, which hands whoever is still writing a SIGPIPE, and pipefail turns that into a failure.
grep -q "PASS no metric dropped" /tmp/stub-compare-run.txt \
  || { echo "  eval-model did not pass against the baseline it just wrote:"; tail -12 /tmp/stub-compare-run.txt; exit 1; }

echo "  ok        the model path runs end to end: a baseline written, a comparison passed"
