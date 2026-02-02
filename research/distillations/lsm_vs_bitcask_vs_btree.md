# LSM Tree vs. Bitcask

This document compares **Log-Structured Merge-Trees (LSM)** and **Bitcask** (Log-Structured Hash Table) to help decide the storage engine for SofaDB.

## At a Glance

| Feature | Bitcask | LSM Tree |
| --- | --- | --- |
| **Primary Index** | In-Memory Hash Map (Key $\to$ File Offset) | Sparse Index (Memory) + Sorted Disk Files (SSTables) |
| **Key Constraint** | **All keys must fit in RAM** | Keys can exceed RAM |
| **Write Type** | Append-only (Sequential) | Append to MemTable (Memory) $\to$ Flush Sequential |
| **Read Latency** | **Low & Predictable** (1 Disk Seek) | Variable (MemTable + Bloom Filter + Multiple SSTables) |
| **Space Amp** | High (until compaction/merge) | Tunable (Leveled vs Tiered compaction) |
| **Range Scans** | **Poor** (Keys in hash map are unordered) | **Excellent** (Data is sorted by key) |

## Common Misconception: Index vs. Storage
Both Bitcask and LSM-Trees are **full storage engines**. They are responsible for:
-  **Persisting Data**: Writing the actual user data (Values) to disk files (`.log` or `.sst`).
-  **Indexing**: Maintaining the data structures (Hash Map or Sparse Index) to find that data efficiently.
They "bear storage responsibilities" completely. You give them a Key and Value, and they ensure it is saved to disk and retrievable.

## Deep Dive

### 1. Bitcask
**Architecture**:
- **Writes**: Always append to the active log file. Very fast.
- **Reads**: Check `KeyDir` (Hash Map) in memory to get file position. Jump to position and read value.
- **Compaction**: "Merge" process reads old files, keeps only latest version of keys, writes to new file.

**Pros**:
- **Simplicity**: Very easy to implement.
- **Speed**: Reads are consistently fast (never more than 1 disk seek). Writes are max disk bandwidth.
- **Crash Recovery**: Trivial (rebuild KeyDir from log files).

**Cons**:
- **Memory Limit**: If your dataset has billions of keys, you need dozens of GBs of RAM just for the keys.
- **Startup Time**: Must scan all files to rebuild KeyDir (unless "Hint Files" are used).
- **No Range Queries**: You cannot efficiently Scan `KeyA` to `KeyZ` because hashing destroys order.

### 2. LSM Tree (LevelDB, RocksDB, Bigtable)
**Architecture**:
- **Writes**: Insert into `MemTable` (in-memory sorted tree). When full, flush to disk as `SSTable`.
- **Reads**: Check MemTable $\to$ Check Bloom Filters $\to$ Search SSTables (Level 0, Level 1...).
- **Compaction**: Background process merges and sorts overlapping SSTables to remove deleted/overwritten data.

**Pros**:
- **Scalability**: Handles datasets much larger than RAM. Only a small sparse index needs to be in memory.
- **Range Queries**: Data is stored sorted, so scanning `KeyA` to `KeyZ` is efficient.
- **Write Throughput**: Converts random writes to sequential disk I/O.

**Cons**:
- **Read Amplification**: May need to check multiple files for a read (mitigated by Bloom Filters).
- **Complexity**: Implementing correct compaction strategies (Leveled vs Tiered) is hard.
- **Write Stalls**: Heavy compaction can temporarily slow down writes.

## Decision for SofaDB

**Choose LSM Tree if:**
- You need **Range Scans** (e.g., "Get all users starting with 'A'").
- The dataset (keys) will grow larger than available RAM.
- You want a general-purpose database suitable for various workloads.

**Choose Bitcask if:**
- Workload is purely **Point Lookups** (`Get(Key)`).
- Keys are guaranteed to fit in RAM.
- Implementation simplicity is the highest priority (e.g., a simple KV store project).

### Recommendation
For a robust database like **SofaDB**, **LSM Tree** is generally the better default choice because it supports range queries and scales better, despite the higher implementation complexity. However, **Bitcask** is an excellent choice for a specific "Blob Store" component where you look up blobs by ID and never scan ranges.
