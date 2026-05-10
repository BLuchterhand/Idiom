APP_NAME ?= idiom
CONFIG ?= config.json
LOCAL_BIN ?= $(HOME)/.local/bin
SUITE ?= testdata/swe/representative-suite.json
LIVE_SUBSET_SUITE ?= testdata/swe/live-subset.json
LIVE_LOGS ?= testdata/swe/generated-logs
CLASSIFIER_BINARY ?= llama-server
CLASSIFIER_MODEL ?= models/qwen35-9b/Qwen3.5-9B-Q8_0.gguf
CLASSIFIER_HOST ?= 127.0.0.1
CLASSIFIER_PORT ?= 11437
CLASSIFIER_CTX ?= 8192
CLASSIFIER_THREADS ?= 0
CLASSIFIER_GPU_LAYERS ?= 0
CLASSIFIER_LOG ?= logs/route-classifier.log
CLASSIFIER_EXTRA_ARGS ?=

.PHONY: run server opencode classifier-server route-classifier build install-path fmt tidy test test-install-docker coverage coverage-report eval swe-eval swe-suite swe-live swe-live-subset

run:
	go run ./cmd/idiom -config $(CONFIG)

server:
	go run ./cmd/idiom -config $(CONFIG) --server-only

opencode:
	./scripts/run-opencode.sh

classifier-server:
	mkdir -p "$(dir $(CLASSIFIER_LOG))"
	$(CLASSIFIER_BINARY) -m "$(CLASSIFIER_MODEL)" --host $(CLASSIFIER_HOST) --port $(CLASSIFIER_PORT) -c $(CLASSIFIER_CTX) -t $(CLASSIFIER_THREADS) --reasoning off --jinja $(if $(filter-out 0,$(CLASSIFIER_GPU_LAYERS)),-ngl $(CLASSIFIER_GPU_LAYERS),) $(CLASSIFIER_EXTRA_ARGS) 2>&1 | tee "$(CLASSIFIER_LOG)"

route-classifier: classifier-server

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/idiom
	$(MAKE) install-path

install-path:
	mkdir -p "$(LOCAL_BIN)"
	ln -sfn "$(CURDIR)/bin/$(APP_NAME)" "$(LOCAL_BIN)/$(APP_NAME)"
	@case ":$$PATH:" in \
		*:"$(LOCAL_BIN)":*) echo "$(LOCAL_BIN)/$(APP_NAME) -> $(CURDIR)/bin/$(APP_NAME)" ;; \
		*) echo "warning: $(LOCAL_BIN) is not on PATH for this shell" ;; \
	esac

fmt:
	go fmt ./...

tidy:
	go mod tidy

test:
	go test ./...

test-install-docker:
	./scripts/test-install-docker.sh

coverage:
	go test ./... -cover

coverage-report:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

eval:
	go run ./cmd/idiom-eval -log $(LOG)

swe-eval:
	go run ./cmd/idiom-swe-eval -suite $(SUITE) -logs $(LOGS)

swe-suite:
	go run ./cmd/idiom-swe-eval -suite $(SUITE) -suite-only

swe-live:
	go run ./cmd/idiom-swe-eval -suite $(SUITE) -run-live -generated-logs $(LIVE_LOGS) -runner-command "$(CURDIR)/scripts/swe-live-runner.sh"

swe-live-subset:
	go run ./cmd/idiom-swe-eval -suite $(LIVE_SUBSET_SUITE) -run-live -generated-logs $(LIVE_LOGS) -runner-command "$(CURDIR)/scripts/swe-live-runner.sh"
