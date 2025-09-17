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

GOFLAGS :=
LDFLAGS := -s -w \
	-X main.version=$(GIT_VERSION) \
	-X main.commit=$(GIT_COMMIT) \
	-X main.date=$(BUILD_DATE)

.PHONY: all build run test fmt vet tidy clean docker-build docker-run docker-push version

all: build

$(BIN):
	@mkdir -p $(BIN)

build: $(BIN)
	GOFLAGS=$(GOFLAGS) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$(APP) ./
	@echo "Built $(BIN)/$(APP) (version=$(GIT_VERSION), commit=$(GIT_COMMIT))"

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
		-t $(IMAGE) .

# Example: make docker-run ARGS="query /data/metrics.prom"
docker-run:
	docker run --rm -it -v "$(ROOT)":/data $(IMAGE) $(ARGS)

docker-push:
	docker push $(IMAGE)

# Show computed version info
version:
	@echo "VERSION=$(GIT_VERSION)"
	@echo "COMMIT=$(GIT_COMMIT)"
	@echo "DATE=$(BUILD_DATE)"

