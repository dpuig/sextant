VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/dpuig/sextant/pkg/version.Version=$(VERSION)

.PHONY: build test cover lint

build:
	go build -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

test:
	go test ./... -race -count=1

cover:
	go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1

lint:
	golangci-lint run ./...
