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

PG_CONTAINER ?= sextant-pg
PG_PORT ?= 54329
CONTAINER ?= podman

.PHONY: test-integration pg-up pg-down

pg-up:
	@$(CONTAINER) container exists $(PG_CONTAINER) || \
		$(CONTAINER) run --rm -d --name $(PG_CONTAINER) -e POSTGRES_PASSWORD=test -p $(PG_PORT):5432 docker.io/library/postgres:17
	@# -h 127.0.0.1: the image's temporary init server listens on a unix socket only.
	@for i in $$(seq 1 60); do \
		$(CONTAINER) exec $(PG_CONTAINER) pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1 && exit 0; sleep 1; \
	done; echo "postgres did not become ready" >&2; exit 1

pg-down:
	-$(CONTAINER) stop $(PG_CONTAINER)

test-integration: pg-up
	SEXTANT_TEST_PG_DSN=postgres://postgres:test@localhost:$(PG_PORT)/postgres go test ./pkg/storage/... -race -count=1

.PHONY: bench-tunnel
bench-tunnel:
	SEXTANT_SPIKE=1 go test ./spikes/tunnel -v -count=1

ARCH ?= $(shell go env GOARCH)
REGISTRY ?= ghcr.io/dpuig/sextant
IMAGE_TAG ?= $(VERSION)

.PHONY: dist images
dist:
	@mkdir -p dist/linux-$(ARCH)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/linux-$(ARCH)/ ./cmd/apiserver ./cmd/agent

images: dist
	$(CONTAINER) build --platform linux/$(ARCH) -f deploy/docker/Dockerfile.apiserver --build-arg DIST=dist/linux-$(ARCH) -t $(REGISTRY)/apiserver:$(IMAGE_TAG) .
	$(CONTAINER) build --platform linux/$(ARCH) -f deploy/docker/Dockerfile.agent --build-arg DIST=dist/linux-$(ARCH) -t $(REGISTRY)/agent:$(IMAGE_TAG) .

.PHONY: e2e
# Needs kind, helm, kubectl and a container runtime (CONTAINER=podman|docker). Builds images tagged e2e, then runs the
# Phase 0 gates against two kind clusters. SEXTANT_E2E_KEEP=1 leaves them up.
e2e:
	$(MAKE) images IMAGE_TAG=e2e
	SEXTANT_E2E_CONTAINER=$(CONTAINER) go test -tags e2e ./test/e2e -run TestPhase0Gates -v -count=1 -timeout 30m
