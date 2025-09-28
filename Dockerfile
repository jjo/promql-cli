# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.24 AS build
WORKDIR /app

# Leverage go mod cache
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source
COPY . .

# Build static binary (no CGO) using Makefile target to keep flags in sync
ENV CGO_ENABLED=0
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
ARG BUILD_TAGS=prompt
# Ensure make is available (usually present, but install if missing)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    bash -c 'command -v make >/dev/null 2>&1 || (apt-get update && apt-get install -y --no-install-recommends make && rm -rf /var/lib/apt/lists/*); \
             make GIT_VERSION=${VERSION} GIT_COMMIT=${COMMIT} BUILD_DATE=${BUILD_DATE} BUILD_TAGS=${BUILD_TAGS} build-binary && \
             install -D -m 0755 bin/promql-cli /out/promql-cli'

# --- runtime stage ---
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/promql-cli /usr/local/bin/promql-cli
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/promql-cli"]
# Default command shows program usage; override with e.g. `query /data/metrics.prom`
CMD [""]
