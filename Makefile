SHELL := /bin/bash
ROOT := $(shell pwd)
BIN := $(ROOT)/bin
APP := promql-cli
PKG := github.com/jjo/promql-cli

TAG ?= latest
IMAGE := xjjo/$(APP):$(TAG)

# Git-derived versioning (falls back for non-git envs)
GIT_VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
GIT_COMMIT  := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Build tags - include prompt support by default
# Can be overridden with: make BUILD_TAGS="" build
BUILD_TAGS ?= prompt

GOFLAGS :=
LDFLAGS := -s -w \
	-X main.version=$(GIT_VERSION) \
	-X main.commit=$(GIT_COMMIT) \
	-X main.date=$(BUILD_DATE)

.PHONY: all build build-binary build-no-prompt run test fmt vet tidy clean docker-build docker-run docker-push version help gofumpt

all: build

$(BIN):
	@mkdir -p $(BIN)

build: $(BIN) build-binary

# Build a single binary at repo root (./promql-cli)
build-binary:
	GOFLAGS=$(GOFLAGS) CGO_ENABLED=0 go build -tags "$(BUILD_TAGS)" -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/cli/...
	@echo "Built $(BIN)/$(APP) (version=$(GIT_VERSION), commit=$(GIT_COMMIT), tags=$(BUILD_TAGS))"

# Build without prompt support (minimal binary)
build-no-prompt:
	BUILD_TAGS="" $(MAKE) build-binary
	@echo "Built minimal binary without go-prompt support"

run: build
	$(BIN)/$(APP) $(ARGS)

fmt:
	go fmt ./...

vet:
	go vet ./...

_test_pkgs := $(shell go list ./... 2>/dev/null)

# No tests currently; this target will succeed if there are no packages
# with tests. It will run anything it finds.
test:
	@if [ -z "$(_test_pkgs)" ]; then echo "No Go packages found for testing"; else go test -v ./...; fi

tidy:
	go mod tidy

clean:
	rm -rf $(BIN)

# Docker

docker-build:
	docker build \
		--build-arg VERSION=$(GIT_VERSION) \
		--build-arg COMMIT=$(shell git rev-parse HEAD 2>/dev/null || echo unknown) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg BUILD_TAGS=$(BUILD_TAGS) \
		-t $(IMAGE) .

# Example: make docker-run ARGS="query /data/metrics.prom"
docker-run:
	docker run --rm -it -v "$(ROOT)":/data $(IMAGE) $(ARGS)

docker-push:
	docker push $(IMAGE)

gofumpt:
	find . -name '*.go' -not -path "./vendor/*" | xargs gofumpt -w

# Show computed version info
version:
	@echo "VERSION=$(GIT_VERSION)"
	@echo "COMMIT=$(GIT_COMMIT)"
	@echo "DATE=$(BUILD_DATE)"
	@echo "BUILD_TAGS=$(BUILD_TAGS)"

# Help target
help:
	@echo "Available targets:"
	@echo "  build           - Build binary with go-prompt support (default)"
	@echo "  build-binary    - Build single binary at repo root"
	@echo "  build-no-prompt - Build minimal binary without go-prompt"
	@echo "  run             - Build and run the application"
	@echo "  test            - Run tests"
	@echo "  fmt             - Format code"
	@echo "  vet             - Run go vet"
	@echo "  tidy            - Run go mod tidy"
	@echo "  clean           - Remove built binaries"
	@echo "  docker-build    - Build Docker image"
	@echo "  docker-run      - Run Docker container"
	@echo "  docker-push     - Push Docker image"
	@echo "  version         - Show version information"
	@echo "  help            - Show this help"
	@echo ""
	@echo "Build options:"
	@echo "  BUILD_TAGS      - Build tags (default: prompt)"
	@echo "                    Use BUILD_TAGS=\"\" for minimal build"
	@echo ""
	@echo "Examples:"
	@echo "  make build                  # Build with prompt support"
	@echo "  make build-no-prompt        # Build without prompt support"
	@echo "  BUILD_TAGS=\"\" make build   # Build without prompt support"
