# SofaDB

A lightweight, file-based key-document database written in Go with an HTTP API.

## Features

- **Simple REST API** - GET, PUT, DELETE documents by key
- **File-based persistence** - Data survives restarts
- **Append-only log** - Fast writes, crash-safe
- **In-memory index** - Fast reads via direct seek
- **Thread-safe** - Handles concurrent requests

## Quick Start

### Build

```bash
go build -o sofadb ./cmd/sofadb
```

### Run

```bash
# Start with defaults (port 8080, data in ./data)
./sofadb

# Custom port and data directory
./sofadb --port 9000 --data-dir /path/to/data
```

### Usage

```bash
# Store a document
curl -X PUT http://localhost:8080/docs/user1 \
  -H "Content-Type: application/json" \
  -d '{"name": "Alice", "email": "alice@example.com"}'

# Retrieve a document
curl http://localhost:8080/docs/user1

# List all keys
curl http://localhost:8080/docs

# Delete a document
curl -X DELETE http://localhost:8080/docs/user1

# Health check
curl http://localhost:8080/health
```

## API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/docs/{key}` | Retrieve a document |
| `PUT` | `/docs/{key}` | Create or update a document |
| `DELETE` | `/docs/{key}` | Delete a document |
| `GET` | `/docs` | List all keys |
| `GET` | `/health` | Health check |

## Storage Design

SofaDB uses an append-only log with an in-memory index:

- **Writes**: Always append to end of file → O(1)
- **Reads**: Look up offset in index, seek to position → O(1)
- **Deletes**: Append tombstone marker → O(1)

On startup, the log is replayed to rebuild the index.

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | HTTP server port |
| `--data-dir` | `./data` | Directory for data files |
| `--version` | - | Show version and exit |

## License

MIT
