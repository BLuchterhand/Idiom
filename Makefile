APP_NAME ?= qwen-bubbles
CONFIG ?= config.json
SUITE ?= testdata/swe/representative-suite.json
LIVE_LOGS ?= testdata/swe/generated-logs
CLASSIFIER_BINARY ?= llama-server
CLASSIFIER_MODEL ?= /Users/ben/models/qwen35-9b/Qwen3.5-9B-Q8_0.gguf
CLASSIFIER_HOST ?= 127.0.0.1
CLASSIFIER_PORT ?= 11437
CLASSIFIER_CTX ?= 8192
CLASSIFIER_THREADS ?= 0
CLASSIFIER_GPU_LAYERS ?= 0
CLASSIFIER_LOG ?= logs/route-classifier.log
CLASSIFIER_EXTRA_ARGS ?=

.PHONY: run server opencode classifier-server route-classifier build fmt tidy test coverage coverage-report eval swe-eval swe-suite swe-live

run:
	go run ./cmd/qwen-bubbles -config $(CONFIG)

server:
	go run ./cmd/qwen-bubbles -config $(CONFIG) --server-only

opencode:
	./scripts/run-opencode.sh

classifier-server:
	mkdir -p "$(dir $(CLASSIFIER_LOG))"
	$(CLASSIFIER_BINARY) -m "$(CLASSIFIER_MODEL)" --host $(CLASSIFIER_HOST) --port $(CLASSIFIER_PORT) -c $(CLASSIFIER_CTX) -t $(CLASSIFIER_THREADS) --reasoning off --jinja $(if $(filter-out 0,$(CLASSIFIER_GPU_LAYERS)),-ngl $(CLASSIFIER_GPU_LAYERS),) $(CLASSIFIER_EXTRA_ARGS) 2>&1 | tee "$(CLASSIFIER_LOG)"

route-classifier: classifier-server

build:
	go build -o bin/$(APP_NAME) ./cmd/qwen-bubbles

fmt:
	go fmt ./...

tidy:
	go mod tidy

test:
	go test ./...

coverage:
	go test ./... -cover

coverage-report:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

eval:
	go run ./cmd/qwen-eval -log $(LOG)

swe-eval:
	go run ./cmd/qwen-swe-eval -suite $(SUITE) -logs $(LOGS)

swe-suite:
	go run ./cmd/qwen-swe-eval -suite $(SUITE) -suite-only

swe-live:
	go run ./cmd/qwen-swe-eval -suite $(SUITE) -run-live -generated-logs $(LIVE_LOGS) -runner-command "$(CURDIR)/scripts/swe-live-runner.sh"
