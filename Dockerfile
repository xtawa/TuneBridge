# Multi-stage Go build
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Download dependencies using cache mounts / layer caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code and embedded assets
COPY . .

# Build statically-linked executable
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /usr/local/bin/tunebridge ./cmd/tunebridge

# Final runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /data

COPY --from=builder /usr/local/bin/tunebridge /usr/local/bin/tunebridge

ENV TUNEBRIDGE_DATA_DIR=/data \
    TUNEBRIDGE_LISTEN_ADDRESS=:8080

VOLUME ["/data"]

EXPOSE 8080

# Explicit healthcheck using built-in busybox wget in Alpine without extra external dependencies
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/tunebridge"]
