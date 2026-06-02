APP_NAME ?= idiom
CONFIG ?= config.json
LOCAL_BIN ?= $(HOME)/.local/bin
CANONICAL_BIN ?= $(HOME)/.idiom/bin
CANONICAL_APP := $(CANONICAL_BIN)/$(APP_NAME)
REPO_APP := $(CURDIR)/bin/$(APP_NAME)
ROOT_APP := $(CURDIR)/$(APP_NAME)
QUEUE_LABEL ?= com.idiom.queue
QUEUE_SOCKET ?= $(HOME)/.idiom/runner.sock
SUITE ?= testdata/swe/representative-suite.json
LIVE_SUBSET_SUITE ?= testdata/swe/live-subset.json
LIVE_LOGS ?= .idiom/swe-generated-logs
LIVE_CONTRACT_SUITE ?= testdata/live-contracts/suite.json
LIVE_CONTRACT_LOGS ?= testdata/live-contracts/generated-logs
SCRIPTED_CONTRACT_SUITE ?= testdata/scripted-contracts/suite.json
SCRIPTED_CONTRACT_LOGS ?= testdata/scripted-contracts/generated-logs
CLASSIFIER_BINARY ?= llama-server
CLASSIFIER_MODEL ?= models/qwen3.6-35b-a3b/Qwen3.6-35B-A3B-Q4_K_M.gguf
CLASSIFIER_HOST ?= 127.0.0.1
CLASSIFIER_PORT ?= 11437
CLASSIFIER_CTX ?= 8192
CLASSIFIER_THREADS ?= 0
CLASSIFIER_GPU_LAYERS ?= 0
CLASSIFIER_LOG ?= logs/route-classifier.log
CLASSIFIER_EXTRA_ARGS ?=

.PHONY: run server opencode classifier-server route-classifier build install-path refresh-queue-daemon fmt tidy test test-install-docker test-install-tools coverage coverage-report eval swe-eval swe-suite swe-live swe-live-subset live-contract-suite live-contracts scripted-contract-suite scripted-contracts lint lint-vet

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
	mkdir -p "$(CANONICAL_BIN)"
	go build -o "$(CANONICAL_APP)" ./cmd/idiom
	@if [ "$$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then \
		codesign -s - "$(CANONICAL_APP)" 2>/dev/null || true; \
	fi
	$(MAKE) install-path
	$(MAKE) refresh-queue-daemon

install-path:
	mkdir -p "$(CANONICAL_BIN)" "$(LOCAL_BIN)" "$(CURDIR)/bin"
	@if [ ! -x "$(CANONICAL_APP)" ]; then \
		if [ -x "$(REPO_APP)" ] && ! [ "$(REPO_APP)" -ef "$(CANONICAL_APP)" ]; then \
			cp -f "$(REPO_APP)" "$(CANONICAL_APP)"; \
			if [ "$$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then \
				codesign -s - "$(CANONICAL_APP)" 2>/dev/null || true; \
			fi; \
		else \
			echo "error: $(CANONICAL_APP) is missing; run make build first" >&2; \
			exit 1; \
		fi; \
	fi
	ln -sfn "$(CANONICAL_APP)" "$(REPO_APP)"
	ln -sfn "$(CANONICAL_APP)" "$(ROOT_APP)"
	ln -sfn "$(CANONICAL_APP)" "$(LOCAL_BIN)/$(APP_NAME)"
	@echo "canonical: $(CANONICAL_APP)"
	@echo "$(REPO_APP) -> $(CANONICAL_APP)"
	@echo "$(ROOT_APP) -> $(CANONICAL_APP)"
	@case ":$$PATH:" in \
		*:"$(LOCAL_BIN)":*) echo "$(LOCAL_BIN)/$(APP_NAME) -> $(CANONICAL_APP)" ;; \
		*) echo "warning: $(LOCAL_BIN) is not on PATH for this shell" ;; \
	esac

refresh-queue-daemon:
	@if [ "$$(uname -s)" != "Darwin" ] || ! command -v launchctl >/dev/null 2>&1; then \
		exit 0; \
	fi; \
	domain="gui/$$(id -u)"; \
	if ! launchctl print "$$domain/$(QUEUE_LABEL)" >/dev/null 2>&1; then \
		exit 0; \
	fi; \
	state="$$( \
		"$(CANONICAL_APP)" queue status --socket "$(QUEUE_SOCKET)" --json 2>/dev/null \
		| python3 -c 'import json,sys; data=json.load(sys.stdin); busy=sum(p.get("in_flight",0)+p.get("waiting",0) for p in data.get("pools",[])); print("busy" if busy else "idle")' 2>/dev/null \
		|| echo unreachable \
	)"; \
	case "$$state" in \
		busy) \
			echo "warning: $(QUEUE_LABEL) has active or waiting jobs; not restarting queue daemon" ;; \
		*) \
			if launchctl kickstart -k "$$domain/$(QUEUE_LABEL)" >/dev/null 2>&1; then \
				for i in 1 2 3 4 5 6 7 8 9 10; do \
					"$(CANONICAL_APP)" queue ping --socket "$(QUEUE_SOCKET)" >/dev/null 2>&1 && break; \
					sleep 1; \
				done; \
				if "$(CANONICAL_APP)" queue ping --socket "$(QUEUE_SOCKET)" >/dev/null 2>&1; then \
					echo "restarted $(QUEUE_LABEL) to use $(CANONICAL_APP)"; \
				else \
					echo "warning: restarted $(QUEUE_LABEL), but queue daemon is not reachable yet" >&2; \
				fi; \
			else \
				echo "warning: unable to restart $(QUEUE_LABEL); run: launchctl kickstart -k $$domain/$(QUEUE_LABEL)" >&2; \
			fi ;; \
	esac

fmt:
	go fmt ./...

tidy:
	go mod tidy

test:
	go test ./...

test-install-docker:
	./scripts/test-install-docker.sh

test-install-tools:
	./scripts/test-install-tools-local.sh

test-install-queue-daemon:
	./scripts/test-install-queue-daemon.sh

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

swe-live: build
	IDIOM_BIN="$(CANONICAL_APP)" go run ./cmd/idiom-swe-eval -suite $(SUITE) -run-live -run-live-continue-on-error -generated-logs $(LIVE_LOGS) -runner-command "$(CURDIR)/scripts/swe-live-runner.sh"

swe-live-subset: build
	IDIOM_BIN="$(CANONICAL_APP)" go run ./cmd/idiom-swe-eval -suite $(LIVE_SUBSET_SUITE) -run-live -run-live-continue-on-error -generated-logs $(LIVE_LOGS) -runner-command "$(CURDIR)/scripts/swe-live-runner.sh"

live-contract-suite:
	go run ./cmd/idiom-swe-eval -suite $(LIVE_CONTRACT_SUITE) -suite-only

live-contracts: build
	IDIOM_BIN="$(CANONICAL_APP)" go run ./cmd/idiom-swe-eval -suite $(LIVE_CONTRACT_SUITE) -run-live -generated-logs $(LIVE_CONTRACT_LOGS) -runner-command "$(CURDIR)/scripts/live-contract-runner.sh"

scripted-contract-suite:
	go run ./cmd/idiom-swe-eval -suite $(SCRIPTED_CONTRACT_SUITE) -suite-only

scripted-contracts: build
	IDIOM_BIN="$(CANONICAL_APP)" go run ./cmd/idiom-swe-eval -suite $(SCRIPTED_CONTRACT_SUITE) -run-live -run-live-continue-on-error -generated-logs $(SCRIPTED_CONTRACT_LOGS) -runner-command "$(CURDIR)/scripts/scripted-contract-runner.sh"

lint: lint-vet

lint-vet:
	go vet ./...
