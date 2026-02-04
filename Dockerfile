# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
COPY . .
RUN go build -o sofadb ./cmd/sofadb

# Run stage
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/sofadb .
EXPOSE 8080
CMD ["./sofadb", "-port", "8080", "-data-dir", "/data"]
