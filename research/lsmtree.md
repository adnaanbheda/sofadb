# The Log-Structured Merge-Tree (LSM-Tree)

## Overview
The LSM-Tree is a disk-based data structure designed to provide low-cost indexing for files experiencing a high rate of record inserts (and deletes) over an extended period. It avoids the high I/O cost of random index updates in B-trees by batching changes in memory and cascading them to disk.

## Architecture
- **Multi-Component Structure**: An LSM-tree consists of $K+1$ components: $C_0, C_1, \dots, C_K$.
- **$C_0$ Component**:
    - Memory-resident.
    - Can use any efficient in-memory tree structure (e.g., AVL tree, 2-3 tree).
    - Receives all new inserts/updates.
    - Has no I/O cost for insertion.
- **$C_1 \dots C_K$ Components**:
    - Disk-resident.
    - Optimized for sequential disk access (100% full nodes, multi-page blocks).
    - Increasing in size (typically in a geometric progression).

## The Rolling Merge
- **Mechanism**: When $C_0$ reaches a threshold size, a "rolling merge" process moves entries from $C_0$ to $C_1$. Similarly, when $C_i$ fills, entries merge into $C_{i+1}$.
- **Batching**: The key advantage is that multiple updates are written to disk in a single sequential write operation (multi-page block), amortizing the I/O cost.
- **Cursor**: A conceptual cursor circulates through the key space, merging entries from the smaller component to the larger one.

## Read/Write Operations
- **Insert**: Insert into $C_0$. If $C_0$ is full, trigger merge.
- **Delete**: Insert a "delete node" (tombstone) into $C_0$. This migrates down and annihilates the target entry when they meet.
- **Find**: Check $C_0$, then $C_1$, ..., then $C_K$.
    - **Optimization**: Use Bloom filters or time-ranges to avoid searching all components.
    - **Note**: Reads can be slower than B-trees if the entry is in a lower component, but for "hot" recently inserted data, $C_0$ serves as a cache.

## Performance Analysis
- **Five Minute Rule**: Used to justify memory residence.
- **Cost**: LSM-tree reduces disk arm cost for inserts by orders of magnitude compared to B-trees.
- **Trade-off**: Improves write performance significantly at the cost of potentially slower reads (if not found in $C_0$) and higher space amplification (storing multiple versions/tombstones until merge).

## Relevance to SofaDB
- **Core Storage Engine**: The LSM-tree is the standard storage engine for write-heavy workloads (used in Bigtable, LevelDB, RocksDB, Cassandra).
- **Writes**: Ideally suited for capturing high-throughput data streams.
- **Components**: Implementing a 2-component ($C_0$, $C_1$) system is a good starting point (MemTable + SSTables).
