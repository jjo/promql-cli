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

# Build static binary (no CGO)
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w" -o /out/inmem-promql ./

# --- runtime stage ---
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/inmem-promql /usr/local/bin/inmem-promql
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/inmem-promql"]
# Default command shows program usage; override with e.g. `query /data/metrics.prom`
CMD [""]

