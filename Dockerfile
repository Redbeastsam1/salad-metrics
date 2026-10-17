# ==========================================
# Multi-stage Dockerfile for Salad Metrics Hub
# ==========================================

# ── Stage 1: Build the static Go binary ──
FROM golang:1.22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

# Cache Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree
COPY . .

# Build static Linux binary (pure Go, zero CGO needed)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o salad-metrics .

# ── Stage 2: Production runtime image (~20MB) ──
FROM alpine:3.20

WORKDIR /app

# CA certs for Discord bot / exchange rates, tzdata for local time
RUN apk add --no-cache ca-certificates tzdata

# Copy binary & web assets
COPY --from=builder /app/salad-metrics /app/salad-metrics
COPY templates /app/templates
COPY static /app/static

# Persistent data directory for SQLite database
RUN mkdir -p /app/data

EXPOSE 8000

ENTRYPOINT ["/app/salad-metrics"]
CMD ["-port", "8000", "-tray=false", "-db", "/app/data/salad_metrics.db"]
