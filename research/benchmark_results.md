# Benchmark Results: SofaDB vs MongoDB vs Redis

## Environment
*   **Concurrency**: 20 Clients
*   **Requests**: 10,000 per DB
*   **Platform**: Docker (WSL2)
*   **Client**: Go HTTP/Native Clients

## Results

| Database | Operation | Time (Total) | QPS (Approx) | Relative Perf |
| --- | --- | --- | --- | --- |
| **Redis** | Write | 0.80s | **12,557** | 1.0x (Baseline) |
|  | Read | 0.80s | **12,511** | 1.0x (Baseline) |
| **MongoDB** | Write | 0.93s | **10,806** | ~0.86x |
|  | Read | 14.60s | **685** | ~0.05x |
| **SofaDB (HTTP)** | Write | 3.48s | **2,870** | ~0.23x |
|  | Read | 21.30s | **469** | ~0.03x |
| **SofaDB (TCP)** | Write | 0.91s | **10,970** | **~0.87x** |
|  | Read | 0.92s | **10,901** | **~0.87x** |

## Analysis

### 1. Write Performance (SofaDB vs Redis)
*   **SofaDB is ~4x slower than Redis/Mongo.**
*   **Why?**
    *   **HTTP Overhead**: We use `net/http` JSON handling, which adds significant CPU cost compared to Redis's raw TCP or Mongo's extensive driver optimization.
    *   **WAL Sync**: We might be hitting disk often, whereas Redis is in-memory and Mongo uses group commit.
    *   **Global Lock**: SofaDB uses a global `sync.RWMutex` which serializes writes, whereas Mongo and Redis (single thread event loop) handle concurrency differently.

### 2. Read Performance
*   **SofaDB is ~30x slower than Redis.**
*   **Why?**
    *   **No Cache**: Every read checks the MemTable, then **scans disk** (SSTables). Any miss (random keys) forces a full disk check.
    *   **Missing Bloom Filters**: This is the major culprit. Without Bloom Filters, we do unnecessary disk I/O for every key lookup.
*   **MongoDB Note**: Mongo read QPS (655) seems low here. This might be due to the random nature of keys forcing B-Tree page faults in a cold cache scenario, or the test setup.

## Conclusion
SofaDB is functional but needs optimization to compete.
*   **Immediate Fix**: Implement **Bloom Filters** to fix Read performance.
*   **Secondary Fix**: Implement **Block Cache** to reduce disk I/O.
