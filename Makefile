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
SCRIPTED_CONTRACT_CI_TASKS := \
	scripted-chat-no-workspace-tools \
	scripted-information-readme-grounded-answer \
	scripted-information-directory-ranged-citation \
	scripted-direct-answer-varied-search-model-closeout \
	scripted-coding-help-test-advice-read-only \
	scripted-verification-only-runs-tests-without-edits \
	scripted-workspace-tool-rules-deny-read-file \
	scripted-classifier-ambiguous-question-read-only \
	scripted-classifier-locate-short-label \
	scripted-classifier-architecture-fixture-boundary \
	scripted-classifier-security-review-label \
	scripted-classifier-explore-then-implement-route \
	scripted-classifier-natural-language-implement-short-label \
	scripted-classifier-slash-implement-short-label \
	scripted-implement-multi-path-pending-verification-writes \
	scripted-implement-out-of-scope-write-recovers \
	scripted-plan-execute-submit-ready-read-remains-callable \
	scripted-plan-execute-multi-required-source-before-history \
	scripted-implement-source-prerequisite-fresh-request \
	scripted-implement-authorized-outline-before-write \
	scripted-implement-repeated-target-reads-remain-callable \
	scripted-implement-authorized-discovery-before-write \
	scripted-implement-stale-ranged-citation-refresh \
	scripted-implement-post-write-read-before-verification \
	scripted-implement-post-write-python-command-verification \
	scripted-implement-task-ledger-typed-blocker-replaces-spoof \
	scripted-implement-invalid-write-zero-change-closeout \
	scripted-implement-shell-admission-deny-mutate-allow-read \
	scripted-feature-planner-single-question-open \
	scripted-feature-planner-tdd-sdd-atom-plan \
	scripted-feature-planner-tui-plan-execute-delivery \
	scripted-product-create-tui-prd-workflow
CLASSIFIER_BINARY ?= llama-server
CLASSIFIER_MODEL ?= models/local-model.gguf
CLASSIFIER_HOST ?= 127.0.0.1
CLASSIFIER_PORT ?= 11437
CLASSIFIER_CTX ?= 8192
CLASSIFIER_THREADS ?= 0
CLASSIFIER_GPU_LAYERS ?= 0
CLASSIFIER_LOG ?= logs/route-classifier.log
CLASSIFIER_EXTRA_ARGS ?=
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= $(CURDIR)/bin/golangci-lint
GINKGO_E2E_PACKAGES := \
	./internal/capabilities/mcp \
	./internal/capabilities/workspace \
	./internal/evidence/transcript \
	./internal/platform/commandexec \
	./internal/platform/gitstate \
	./internal/repository/planartifactstore \
	./internal/runtime/agent \
	./internal/runtime/llama \
	./internal/surfaces/cli/e2e \
	./internal/surfaces/cli/featureplanner \
	./internal/surfaces/cli/mcp \
	./internal/surfaces/cli/planexecute \
	./internal/surfaces/cli/queue \
	./internal/surfaces/cli/recover \
	./internal/surfaces/cli/refine \
	./internal/surfaces/cli/root \
	./internal/surfaces/tui/app \
	./internal/workflows/featureplanner \
	./internal/workflows/featureplannerproduct \
	./internal/workflows/featureplannerruntime \
	./internal/workflows/implementationdispatch \
	./internal/workflows/intentloop \
	./internal/workflows/planexecute \
	./internal/workflows/planexecute/adapters \
	./internal/workflows/planexecute/runner \
	./internal/workflows/postflight \
	./internal/workflows/productworkflow \
	./internal/workflows/recovery
GINKGO_SMOKE_PACKAGES := \
	./internal/capabilities/mcp \
	./internal/capabilities/workspace \
	./internal/evidence/transcript \
	./internal/platform/commandexec \
	./internal/repository/planartifactstore \
	./internal/runtime/agent \
	./internal/surfaces/cli/featureplanner \
	./internal/surfaces/cli/mcp \
	./internal/surfaces/cli/queue \
	./internal/surfaces/cli/recover \
	./internal/surfaces/cli/root \
	./internal/surfaces/tui/app \
	./internal/workflows/featureplannerproduct \
	./internal/workflows/planexecute/runner \
	./internal/workflows/productworkflow
GINKGO_RACE_CONTRACT_PACKAGES := \
	./internal/capabilities/mcp \
	./internal/capabilities/workspace \
	./internal/evidence/transcript \
	./internal/platform/commandexec \
	./internal/runtime/agent \
	./internal/runtime/runner \
	./internal/runtime/session \
	./internal/surfaces/cli/featureplanner \
	./internal/surfaces/cli/queue \
	./internal/surfaces/cli/root \
	./internal/surfaces/cli/runtime \
	./internal/surfaces/cli/telemetry \
	./internal/surfaces/tui/app \
	./internal/workflows/featureplannerproduct \
	./internal/workflows/productworkflow
GINKGO_DEVELOPMENT_SCENARIO_PACKAGES := \
	./internal/capabilities/mcp \
	./internal/capabilities/workspace \
	./internal/evidence/transcript \
	./internal/platform/commandexec \
	./internal/runtime/agent \
	./internal/surfaces/cli/featureplanner \
	./internal/surfaces/cli/mcp \
	./internal/surfaces/cli/queue \
	./internal/surfaces/cli/refine \
	./internal/surfaces/cli/root \
	./internal/surfaces/tui/app \
	./internal/workflows/featureplanner \
	./internal/workflows/featureplannerproduct \
	./internal/workflows/featureplannerruntime \
	./internal/workflows/implementationdispatch \
	./internal/workflows/intentloop \
	./internal/workflows/planexecute \
	./internal/workflows/planexecute/adapters \
	./internal/workflows/planexecute/runner \
	./internal/workflows/postflight \
	./internal/workflows/productworkflow \
	./internal/workflows/recovery
MOCKGEN_SOURCES := $(shell find internal -name '*.go' -type f -exec grep -l '^//go:generate go tool mockgen ' {} + | sort)
MOCKGEN_PACKAGES := $(sort $(dir $(MOCKGEN_SOURCES)))
MOCKGEN_OUTPUTS := $(shell for source in $(MOCKGEN_SOURCES); do \
	destination=$$(sed -n 's/.* -destination=\([^ ]*\).*/\1/p' "$$source"); \
	printf '%s%s ' "$$(dirname "$$source")/" "$$destination"; \
	done)
SYSTEM_GINKGO := go tool ginkgo --tags=system_test --fail-fast
SYSTEM_GINKGO_PACKAGE := ./internal/systemtests

.PHONY: run server opencode classifier-server route-classifier build install-path refresh-queue-daemon fmt tidy generate-mocks verify-mocks test test-smoke test-e2e test-development-scenarios test-live test-race-contracts test-system-hermetic test-system-architecture test-install-dependencies-manifest test-install-dependencies-powershell-manifest test-install-dependencies test-install-dependencies-powershell test-install-discovery-tools test-post-install-harness test-install-docker test-install-macos test-install-windows test-install-tools test-install-queue-daemon coverage coverage-report eval swe-eval swe-suite swe-live swe-live-subset live-contract-suite live-contracts scripted-contract-suite scripted-contracts scripted-contracts-ci lint lint-vet lint-style install-golangci-lint desktop-install desktop-dev desktop-build

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

desktop-install:
	cd desktop && npm install

desktop-dev:
	cd desktop && npm run tauri:dev

desktop-build:
	cd desktop && npm run tauri:build

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

generate-mocks:
	go generate $(addprefix ./,$(MOCKGEN_PACKAGES))

verify-mocks: generate-mocks
	git diff --exit-code -- $(MOCKGEN_OUTPUTS)
	@untracked="$$(git ls-files --others --exclude-standard -- $(MOCKGEN_OUTPUTS))"; \
	if [ -n "$$untracked" ]; then \
		echo "untracked generated mocks:" >&2; \
		echo "$$untracked" >&2; \
		exit 1; \
	fi

test:
	go test ./...

test-smoke:
	go tool ginkgo --label-filter='smoke && !live' --fail-on-empty $(GINKGO_SMOKE_PACKAGES)
	go tool ginkgo --label-filter='smoke && !live' --fail-on-empty ./internal/runtime/backends

test-e2e:
	go tool ginkgo --label-filter='(e2e || integration) && !live' --fail-on-empty $(GINKGO_E2E_PACKAGES)
	go tool ginkgo --tags=integration --label-filter='integration && !live' --fail-on-empty ./internal/runtime/backends
	go tool ginkgo --tags=system_test --label-filter='(e2e || integration) && !live && !external-toolchain' --fail-on-empty ./internal/systemtests

test-development-scenarios:
	go tool ginkgo --label-filter='development-scenario && !live' --fail-on-empty $(GINKGO_DEVELOPMENT_SCENARIO_PACKAGES)

test-live:
	go tool ginkgo --tags=integration --label-filter='live' --fail-on-empty ./internal/surfaces/tui/app
	go tool ginkgo --tags=qwen_e2e --label-filter='live' --fail-on-empty ./internal/runtime/agent
	go tool ginkgo --tags=system_test --label-filter='live' --fail-on-empty ./internal/systemtests

test-race-contracts:
	go tool ginkgo --race --label-filter='race-contract' --fail-on-empty $(GINKGO_RACE_CONTRACT_PACKAGES)

test-system-hermetic:
	$(SYSTEM_GINKGO) --label-filter='system && !live && !external-toolchain' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-system-architecture:
	go tool ginkgo --fail-fast --label-filter='system-harness-architecture' --fail-on-empty ./internal/contracts/compile

test-install-dependencies-manifest:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-dependencies-unix-manifest' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-dependencies-powershell-manifest:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-dependencies-powershell-manifest' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-dependencies:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-dependencies-unix-live' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-dependencies-powershell:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-dependencies-powershell-live' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-discovery-tools:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-discovery-tools' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-post-install-harness:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-post-install-harness' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-docker:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-docker' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-macos:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-macos' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-windows:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-windows-integrity' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-tools:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-tools-local' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)

test-install-queue-daemon:
	IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-queue-daemon-static' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE)
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		IDIOM_SYSTEM_TARGETED=1 $(SYSTEM_GINKGO) --label-filter='system-install-queue-daemon-live' --fail-on-empty $(SYSTEM_GINKGO_PACKAGE); \
	fi

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

scripted-contracts-ci:
	go tool ginkgo --label-filter='scripted-contract-python-contract' --fail-on-empty ./internal/surfaces/cli/sweeval
	python3 -m py_compile scripts/scripted-model-server.py scripts/select-scripted-contract-tasks.py
	bash -n scripts/scripted-contract-runner.sh scripts/live-contract-runner.sh
	go run ./cmd/idiom-swe-eval -suite $(SCRIPTED_CONTRACT_SUITE) -suite-only
	@set -eu; \
		suite_file="$$(mktemp "$(CURDIR)/testdata/scripted-contracts/.selected-suite.XXXXXX")"; \
		logs_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/idiom-scripted-contract-logs.XXXXXX")"; \
		idiom_bin="$$(mktemp "$${TMPDIR:-/tmp}/idiom-scripted-contract-bin.XXXXXX")"; \
		rm -f "$$idiom_bin"; \
		trap 'rm -f "$$suite_file" "$$idiom_bin"; rm -rf "$$logs_dir"' EXIT INT TERM; \
		python3 scripts/select-scripted-contract-tasks.py \
			--suite "$(SCRIPTED_CONTRACT_SUITE)" \
			--output "$$suite_file" \
			--require-canonical-coverage \
			$(foreach task,$(SCRIPTED_CONTRACT_CI_TASKS),--task $(task)); \
		go build -o "$$idiom_bin" ./cmd/idiom; \
		IDIOM_SWE_TASK_TIMEOUT=60 IDIOM_BIN="$$idiom_bin" go run ./cmd/idiom-swe-eval \
			-suite "$$suite_file" \
			-run-live \
			-generated-logs "$$logs_dir" \
			-runner-command "$(CURDIR)/scripts/scripted-contract-runner.sh"

lint: lint-vet lint-style

lint-vet:
	go vet ./...

lint-style: install-golangci-lint
	"$(GOLANGCI_LINT)" run ./...

install-golangci-lint:
	@if [ -x "$(GOLANGCI_LINT)" ]; then \
		exit 0; \
	fi; \
	mkdir -p "$(dir $(GOLANGCI_LINT))"; \
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(dir $(GOLANGCI_LINT))" "$(GOLANGCI_LINT_VERSION)"
