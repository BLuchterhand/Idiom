APP_NAME ?= qwen-bubbles
CONFIG ?= config.json
SUITE ?= testdata/swe/representative-suite.json

.PHONY: run server build fmt tidy test coverage coverage-report eval swe-eval swe-suite

run:
	go run ./cmd/qwen-bubbles -config $(CONFIG)

server:
	go run ./cmd/qwen-bubbles -config $(CONFIG) --server-only

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
