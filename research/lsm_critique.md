# LSM Implementation Critique & Roadmap

## Overview
We have implemented a functional Log-Structured Merge (LSM) Tree storage engine for SofaDB. This document analyzes its robustness by comparing it to production-grade systems like **LevelDB** and **RocksDB**.

## Comparison Matrix

| Feature | SofaDB (Current) | LevelDB / RocksDB (Production) | Gap Analysis |
| :--- | :--- | :--- | :--- |
| **MemTable** | Skiplist (single) | Skiplist (Concurrent) | **Moderate**: Our single-threaded insert under lock is simple but limits write concurrency. |
| **Persistence** | WAL (Basic) | WAL (Batch + Group Commit) | **Low**: Our WAL captures data. Missing checksums and batch optimizations. |
| **SSTable Format** | KV + Sparse Index | KV + Index + Bloom Filter + Compression | **High**: Missing **Bloom Filters** means every missed read hits disk. Missing compression wastes space. |
| **Compaction** | Size-Tiered (Merge All) | Leveled (LCS) / Universal | **Critical**: Our compaction merges *everything* into one file. This causes huge Write Amplification. LevelDB splits data into Levels (L0...L6) to minimize rewrite cost. |
| **Concurrency** | Global `RWMutex` | MVCC, Snapshot Isolation | **Critical**: Readers block writers during some operations. Production systems use MVCC so readers never block writers. |
| **Safety** | Basic Error Handling | Checksums (CRC32), MANIFEST file | **High**: We rely on file globbing. A `MANIFEST` file is needed to atomically track valid SSTables and prevent corruption. |
| **Caching** | OS Page Cache | Block Cache (LRU) | **Moderate**: OS cache is good for now, but application-level Block Cache allows better tuning. |

## Robustness Verdict
**Status: Functional Prototype / "Toy" DB**

The current implementation is **correct** in terms of data storage and retrieval semantics. It persists data and handles range scans. However, it is **not yet robust** enough for high-scale production usage due to:
1.  **Performance Cliffs (Disk I/O)**: The "Merge All" strategy causes massive **Write Amplification**. Compacting 1GB of data requires reading and re-writing the entire 1GB, even if only 1KB changed. This saturates **Disk I/O** bandwidth, causing latency spikes for concurrent writes to the WAL. (Note: RAM is no longer a bottleneck due to Streaming Compaction).
2.  **Read Amplification**: Without Bloom Filters, checking non-existent keys is slow.
3.  **Concurrency Bottlenecks**: Global lock limits throughput on multi-core concurrent workloads.

## Roadmap to Production
To reach "Production Grade", the following upgrades are prioritized:

1.  **Implement Bloom Filters**: Drastically reduce disk seeks for `Get`.
2.  **Leveled Compaction**: Switch from "Merge All" to LevelDB-style compaction (L0->L1->L2).
3.  **MANIFEST File**: Track file metadata safely instead of relying on `*.sst` file listing.
4.  **CRC32 Checksums**: Verify data integrity on read to detect disk corruption.

## Comparison with MongoDB (WiredTiger)

MongoDB's default storage engine, **WiredTiger**, is primarily a **B-Tree** engine (though it supports LSM, B-Tree is the default for general workloads).

| Metric | SofaDB (LSM) | MongoDB (B-Tree) | Analysis |
| :--- | :--- | :--- | :--- |
| **Write Amplification** | **Low** | **High** | LSM appends sequentially. B-Trees rewrite entire 4KB-16KB pages for small updates. |
| **Read Amplification** | **High** (without optimizations) | **Low** | B-Trees have predictable O(log N) lookups. LSMs may need to check multiple files (L0...LN). |
| **Space Amplification** | **Medium** | **Low** (via Fragmentation) | LSMs store stale data until compaction. B-Trees fragment but generally reclaim space faster on update-in-place. |
| **Workload Fit** | **Write-Heavy** (Logs, Events, Time-Series) | **Read-Heavy / Balanced** | SofaDB excels at ingesting data fast. MongoDB excels at random reads and complex queries. |

**Conclusion**: SofaDB chooses LSM to maximize **ingest speed** and simplicty. Implementing Bloom Filters is critical to bridging the read-performance gap with B-Tree systems like MongoDB.
