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
EXPOSE 9696
EXPOSE 9091
CMD ["./sofadb", "-port", "9696", "-tcp-port", "9091", "-data-dir", "/data"]
