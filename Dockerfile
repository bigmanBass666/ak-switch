# =============================================================================
# Stage 1: Builder — compile the Go binary
# =============================================================================
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /alvus ./cmd/alvus/

# =============================================================================
# Stage 2: Runtime — Alpine with busybox (built-in) for HEALTHCHECK
# =============================================================================
FROM alpine:3.19

# Copy CA certificates (needed for outbound HTTPS to upstream APIs)
COPY --from=builder /etc/ssl/certs/ /etc/ssl/certs/
# Copy the Go binary
COPY --from=builder /alvus /alvus

EXPOSE 3000
ENTRYPOINT ["/alvus"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -s http://localhost:3000/health
