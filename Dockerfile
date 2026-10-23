# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install git and ca-certificates if needed for downloading dependencies
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compile static binary without CGO for distroless static runtime
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server cmd/server/main.go

# Final stage: minimal and secure distroless image
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /app/server /server
COPY --from=builder /app/migrations /migrations

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/server"]
