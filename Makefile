SHELL := /bin/bash
ROOT := $(shell pwd)
BIN := $(ROOT)/bin
APP := promql-cli
PKG := github.com/jjo/promql-cli

TAG ?= latest
IMAGE := xjjo/$(APP):$(TAG)

GOFLAGS :=
LDFLAGS := -s -w

.PHONY: all build run test fmt vet tidy clean docker-build docker-run docker-push

all: build

$(BIN):
	@mkdir -p $(BIN)

build: $(BIN)
	GOFLAGS=$(GOFLAGS) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$(APP) ./
	@echo "Built $(BIN)/$(APP)"

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
	docker build -t $(IMAGE) .

# Example: make docker-run ARGS="query /data/metrics.prom"
docker-run:
	docker run --rm -it -v "$(ROOT)":/data $(IMAGE) $(ARGS)

docker-push:
	docker push $(IMAGE)

