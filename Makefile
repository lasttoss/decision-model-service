# Everything runs in a container, so a fresh clone works with no Go installed.
IMAGE ?= golang:1.25
CACHE ?= decide-gocache
RUN   := docker run --rm -v $(PWD):/app -v $(CACHE):/go -w /app $(IMAGE)

API_PORT   ?= 18110
FALL_PORT  ?= 18111
IMG        := decision-model-service:local

CASES      ?= eval/cases.jsonl
BASELINE   ?= eval/baseline-rules.json
THRESHOLD  ?= 0.6

.PHONY: help demo test coverage lint build image eval baseline eval-model server eval-recorded chart diagram

help:
	@echo "make demo          three stories: the measurement, the fallback, the model path"
	@echo "make test          go test -race ./...  (no network, no database, no key)"
	@echo "make eval          score the rule baseline and compare with $(BASELINE)  <- the gate"
	@echo "make baseline      rewrite $(BASELINE) on purpose, after reading the diff"
	@echo "make eval-model    score a real model: DECIDE_MODEL_URL=... DECIDE_MODEL_NAME=..."
	@echo "make eval-recorded score the recorded envelopes, for the parser contract only"
	@echo "make server        the API in the container, on :$(API_PORT)"
	@echo "make coverage      go test -race -coverprofile, then the total"
	@echo "make lint          gofmt + go vet"
	@echo "make image         build, then run the evaluation inside the image"
	@echo "make chart         helm lint --strict, render both branches, kubeconform"
	@echo "make diagram       check docs/figures: committed, exported, not older than its sources"

# --- the demo: three stories, in the order that makes each one visible -------------
#
# 1. the measurement: the rule baseline over the frozen case set, with the hard subset and
#    the abstention count next to the headline number;
# 2. the fallback: the service pointed at an endpoint that is not there, answering anyway
#    and saying that it degraded - and why;
# 3. the model path: the same endpoint contract answered from recorded envelopes, so the
#    path works with no network and no key.
demo:
	@echo "== 1. the measurement (rules, $(CASES)) =="
	@$(RUN) go run ./cmd/decide --mode eval --provider rules --baseline $(BASELINE) --threshold $(THRESHOLD) --mistakes 2
	@docker build -q -t $(IMG) . >/dev/null
	@echo
	@echo "== 2. the fallback: model first, pointed at nothing =="
	@docker run -d --rm --name decide-fallback -p $(FALL_PORT):8080 $(IMG) --mode server \
		--provider model --model-url http://127.0.0.1:9/v1 --model-name does-not-exist --threshold $(THRESHOLD) >/dev/null
	@sleep 1
	@curl -sS -X POST -H 'Content-Type: application/json' -d '{"input":"dial tcp 10.4.2.9:5432: i/o timeout"}' \
		http://localhost:$(FALL_PORT)/v1/decide | python3 -c 'import json,sys; d=json.load(sys.stdin); print(f"  label={{d[\"label\"]}} source={{d[\"source\"]}} degraded={{d[\"degraded\"]}} reason={{d.get(\"degraded_reason\",\"-\")}} total_ms={{d[\"total_ms\"]:.2f}}")'
	@docker stop decide-fallback >/dev/null
	@echo
	@echo "== 3. the model path, from recorded envelopes (no network, no key) =="
	@docker run -d --rm --name decide-recorded -p $(API_PORT):8080 $(IMG) --mode server \
		--provider recorded --threshold $(THRESHOLD) >/dev/null
	@sleep 1
	@for line in "context canceled" "panic: runtime error: nil pointer dereference" "the frobnicator wobbled twice"; do \
		printf '  %-46s -> ' "$$line"; \
		curl -sS -X POST -H 'Content-Type: application/json' -d "{\"input\":\"$$line\"}" http://localhost:$(API_PORT)/v1/decide \
		  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(f"{{d[\"label\"]}}/{{d[\"confidence\"]:.2f}} source={{d[\"source\"]}} tokens={{d.get(\"tokens_in\",0)}}/{{d.get(\"tokens_out\",0)}} degraded={{d[\"degraded\"]}}")'; \
	done
	@docker stop decide-recorded >/dev/null
	@echo
	@echo "gate: the model column is empty until you point it at a real endpoint - make eval-model"

test:
	@$(RUN) go test -race ./...

coverage:
	@$(RUN) sh -c 'go test -race -coverprofile=/tmp/cover.out -covermode=atomic ./... >/dev/null && go tool cover -func=/tmp/cover.out | tail -1'

lint:
	@$(RUN) sh -c 'test -z "$$(gofmt -l .)" && echo "gofmt clean" ; go vet ./... && echo "vet clean"'

build:
	@$(RUN) go build -o /tmp/decide ./cmd/decide && echo "decide builds"

# The image is the deliverable, so the image runs the evaluation: if the case set or the
# baseline were not copied in, this is where it shows.
image:
	@docker build -q -t $(IMG) . >/dev/null
	@docker run --rm $(IMG) --mode eval --provider rules --baseline $(BASELINE) | grep -q "PASS no metric dropped" \
		&& echo "image ok: the evaluation runs inside the image, offline"
	@docker run -d --rm --name dms-image-test -p $(API_PORT):8080 $(IMG) --mode server --provider rules >/dev/null
	@for i in $$(seq 1 20); do curl -fsS http://localhost:$(API_PORT)/healthz >/dev/null 2>&1 && break; sleep 1; done
	@curl -fsS -X POST -H 'Content-Type: application/json' -d '{"input":"dial tcp 10.4.2.9:5432: i/o timeout"}' \
		http://localhost:$(API_PORT)/v1/decide | grep -q '"label":"retryable"' && echo "image ok: it serves decisions"
	@curl -fsS http://localhost:$(API_PORT)/metrics | grep -q "decision_decisions_total 1" && echo "image ok: it counts them"
	@docker stop dms-image-test >/dev/null

# Note on the baseline: in the cluster the evaluation CronJob runs with
# readOnlyRootFilesystem, so it can score but cannot rewrite the baseline it compares against.
# That is the property worth having - the baseline is a reviewed artefact from a checkout, not
# something a container edits in place. A local `docker run` has no such guard and the write
# succeeds, which is why `make baseline` exists for the deliberate case.

# The gate. It compares against the committed baseline and exits non-zero on a drop; the CI
# job also doctors the baseline and asserts the failure, because a gate nobody has seen fail
# is a claim.
eval:
	@$(RUN) go run ./cmd/decide --mode eval --provider rules --baseline $(BASELINE) --threshold $(THRESHOLD)

baseline:
	@$(RUN) go run ./cmd/decide --mode eval --provider rules --baseline $(BASELINE) --threshold $(THRESHOLD) --update-baseline

# Scoring a real model is one command and needs an endpoint. Prices are only used for the
# cost line, and they are passed in rather than baked in, because they change.
eval-model:
	@test -n "$(DECIDE_MODEL_URL)" || { echo "set DECIDE_MODEL_URL (and DECIDE_MODEL_NAME) to score a real model"; exit 2; }
	@$(RUN) go run ./cmd/decide --mode eval --provider model --cases $(CASES) --baseline eval/baseline-model.json \
		--threshold $(THRESHOLD) --mistakes 5 \
		--model-url "$(DECIDE_MODEL_URL)" --model-name "$(DECIDE_MODEL_NAME)" --model-key "$(DECIDE_MODEL_KEY)" \
		--price-in $(PRICE_IN) --price-out $(PRICE_OUT)

# Contract only: the recorded envelopes exercise the transport and the parser, and they are
# not evidence about any model's quality. The header of the file says so too.
eval-recorded:
	@$(RUN) go run ./cmd/decide --mode eval --provider recorded --cases eval/cases-recorded.jsonl \
		--baseline eval/baseline-recorded.json --threshold $(THRESHOLD) --mistakes 5

server:
	@docker build -q -t $(IMG) . >/dev/null
	@docker run --rm -p $(API_PORT):8080 $(IMG) --mode server --provider $(PROVIDER) --threshold $(THRESHOLD) \
		$(if $(DECIDE_MODEL_URL),--model-url "$(DECIDE_MODEL_URL)" --model-name "$(DECIDE_MODEL_NAME)",)

HELM  := docker run --rm -v $(PWD):/apps alpine/helm:3.16.3
KCONF := docker run --rm -i ghcr.io/yannh/kubeconform:latest -strict -summary -kubernetes-version 1.30.0 -
CHARTDIR := charts/decision-model-service

# lint renders defaults only, so both branches are rendered, counted and validated: a
# kubeconform run over an empty document reports success, which is a green gate over nothing.
chart:
	@$(HELM) lint --strict /apps/$(CHARTDIR)
	@$(HELM) template decision-model-service /apps/$(CHARTDIR) > /tmp/dms-default.yaml
	@test $$(grep -c '^kind:' /tmp/dms-default.yaml) -ge 4 || { echo "default branch rendered too few resources"; exit 1; }
	@$(KCONF) < /tmp/dms-default.yaml
	@$(HELM) template decision-model-service /apps/$(CHARTDIR) \
		--set model.url=http://ollama.default.svc:11434/v1 --set model.name=qwen2.5:7b-instruct \
		--set eval.enabled=true --set autoscaling.enabled=true > /tmp/dms-full.yaml
	@test $$(grep -c '^kind:' /tmp/dms-full.yaml) -ge 5 || { echo "full branch rendered too few resources"; exit 1; }
	@$(KCONF) < /tmp/dms-full.yaml
	@echo "chart ok: both branches rendered, counted and validated"

diagram:
	@bash ~/.pi/agent/scripts/diagram-freshness.sh
