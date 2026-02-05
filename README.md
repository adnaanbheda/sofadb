# SofaDB

A lightweight, high-performance LSM-tree based key-document database written in Go.

## Features

- **Standardized API**: Consistent `Read`, `Put`, `Delete`, `ReadKeyRange`, and `BatchPut` operations.
- **LSM-Tree Storage**: Optimized for high write throughput and sequential I/O.
- **Binary TCP Protocol**: Minimal overhead for maximum performance (~88% of Redis speed).
- **Hardened Durability**: 100% pass rate on chaos testing (rapid restarts, WAL recovery).
- **Preliminary Performance**: Outperforms MongoDB in high-concurrency local random-read tests.

## Quick Start

### Build & Run

```bash
go build -o sofadb ./cmd/sofadb
./sofadb --port 9090 --tcp-port 9091 --data-dir ./data
```

### HTTP Usage

```bash
# Store a document
curl -X PUT http://localhost:9090/docs/user1 -d '{"name": "Alice"}'

# Read a document
curl http://localhost:9090/docs/user1

# List all keys
curl http://localhost:9090/docs

# Range Scan
curl "http://localhost:9090/range?start=user1&end=user5"
```

## API Reference

### HTTP API

| Method | Endpoint | Standard Op |
|--------|----------|-------------|
| `GET` | `/docs/{key}` | `Read` |
| `PUT` | `/docs/{key}` | `Put` |
| `DELETE` | `/docs/{key}` | `Delete` |
| `GET` | `/docs` | `Keys` |
| `GET` | `/range?start=a&end=z` | `ReadKeyRange` |

### Binary TCP Protocol (`:9091`)

The TCP protocol uses a simple binary format for maximum efficiency.

| Command | Code | Format |
|---------|------|--------|
| `Put` | `0x01` | `Cmd\|KLen\|Key\|VLen\|Value` |
| `Read` | `0x02` | `Cmd\|KLen\|Key` |
| `Delete` | `0x03` | `Cmd\|KLen\|Key` |
| `ReadKeyRange` | `0x04` | `Cmd\|KLen\|StartKey\|EndKLen\|EndKey` |
| `BatchPut` | `0x05` | `Cmd\|Count\|(KLen\|Key\|VLen\|Value)*N` |

## Performance

SofaDB is designed to compete with industry leaders in local-workload scenarios.

| Database | Operation | QPS (Approx) | Efficiency |
| --- | --- | --- | --- |
| **Redis** | Write | 13,199 | 100% |
| **SofaDB** | Write | **11,598** | **~88%** |
| **MongoDB** | Write | 10,936 | ~83% |

*Preliminary results. Documentation for full benchmarking process available in `/research`.*

## Architecture

SofaDB implements a classic LSM stack:
1. **MemTable**: In-memory skip-list for fast ingestion.
2. **WAL**: Write-Ahead Log for crash recovery.
3. **SSTables**: Sorted String Tables with sparse indexing and binary-packed data.
4. **Compaction**: Background N-way merge to clean tombstones and maintain read performance.

## License

MIT
