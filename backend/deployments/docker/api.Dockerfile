# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Download dependencies first (caching)
COPY go.mod go.sum ./
RUN go mod download -x

# Copy source code
COPY . .

# Build the API binary (static, no CGO)
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /api ./cmd/api

# Final stage – minimal image
FROM alpine:3.19

# Install ca-certificates and timezone data
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy binary and config assets
COPY --from=builder /api .
COPY --from=builder /app/config ./config
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/seed ./seed

# Expose API port
EXPOSE 8080

CMD ["./api"]