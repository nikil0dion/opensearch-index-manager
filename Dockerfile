# Build stage
FROM golang:1.25-alpine AS builder

# Install tzdata in builder stage
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /build

# Copy go.mod and go.sum
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary (important for scratch!)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o opensearch-backup-manager \
    ./cmd/manager

# Runtime stage - minimal scratch image
FROM scratch

# Copy CA certificates from builder (needed for HTTPS)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data from builder (now installed with tzdata package)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy compiled application
COPY --from=builder /build/opensearch-backup-manager /app/opensearch-backup-manager

# Copy default configuration
COPY config/config.yaml /app/config/config.yaml

# Run application
ENTRYPOINT ["/app/opensearch-backup-manager"]

