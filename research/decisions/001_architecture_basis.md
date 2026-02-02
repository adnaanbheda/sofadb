# SofaDB Research Compilation

This document synthesizes key concepts from the researched distributed systems papers (Bigtable, Bitcask, LSM-Tree, Paxos, Raft) and applies them to the design of SofaDB.

## 1. Storage Engine Architecture

### The LSM-Tree (Log-Structured Merge-Tree)
*From: Bigtable, LSM-Tree paper*

**Why use it for SofaDB:**
- **High Write Throughput**: Converts random writes into sequential disk writes. Ideal for heavy ingestion workloads.
- **Architecture**:
    - **MemTable**: In-memory sorted buffer (e.g., Red-Black Tree or Skip List). Receives all writes.
    - **SSTables (Sorted String Tables)**: Immutable disk files. When MemTable fills, it's flushed to disk as an SSTable.
    - **Compaction**: Background process merges multiple SSTables to reclaim space (deleted items) and reduce read amplification (fewer files to search).

**SofaDB Implementation Strategy:**
- Implement a MemTable (Go map or Skip List).
- Implement flushing to create SSTables (simple file format, key-sorted).
- Implement a Leveled or Tiered compaction strategy.

### The Bitcask Alternative (Hash-Indexed Log)
*From: Bitcask paper*

**Why use it (or part of it):**
- **Simplicity**: Extremely easy to implement. Write = Append.
- **Low Latency Reads**: Single disk seek (via in-memory hash map).
- **Limitation**: All keys must fit in RAM.

**SofaDB Decision**:
- If SofaDB targets **document storage** where keys (IDs) are small but values (docs) are large, Bitcask is a distinct possibility for values (Blob Store) while LSM-Tree handles the indices.
- **Recommendation**: Default to LSM-Tree for general purpose adaptability (keys don't fit in RAM), but keep Bitcask in mind for a dedicated "Value Log" if decoupling keys/values (WiscKey approach).

## 2. Data Model

### Bigtable Model
*From: Bigtable paper*

**Key Concepts**:
- **Sparse, Distributed Map**: `(Row, Column, Timestamp) -> Value`.
- **Column Families**: Group data that is accessed/compressed together.
- **Timestamps**: Built-in versioning.

**SofaDB Adaptation**:
- SofaDB is a "Key-Value Document Store".
- **KV**: The primary key maps to the Document.
- **Document**: Can be treated as the "Value" (Bigtable style: uninterpreted bytes) or structured (JSON).
- **Versioning**: Implicit timestamps on writes allow strictly consistent reads (snapshot isolation).

## 3. Distributed Consensus & Replication

### Raft vs. Paxos
*From: Raft, Paxos Made Simple papers*

**Comparision**:
- **Paxos**: The theoretical standard. Flexible but notoriously difficult to implement correctly.
- **Raft**: Designed for understandability. Equivalent power. Strong leader model matches database needs (log replication).

**SofaDB Implementation Strategy**:
- **Choose Raft**: It provides a blueprint for Leader Election and Log Replication.
- **Replication Log**: All database commands (Put, Delete) go to the Leader's Raft Log.
- **State Machine**: The local storage engine (LSM-Tree) acts as the deterministic state machine.
- **Consistency**: Use Raft index to track committed writes. Reads can be served by Leader (with lease check) or Followers (at specific commit index for causal consistency).

## 4. Derived SofaDB Architecture Plan

Based on these papers, here is a proposed high-level architecture for SofaDB:

-  **Node Architecture**: Shared-nothing nodes.
-  **Sharding**: Data partitioned by Key Range (like Bigtable Tablets) or Hash (like Dynamo/Cassandra). Range partitioning (Bigtable) supports scans better.
-  **Local Storage**:
    - **Write Path**: WAL (Write Ahead Log) -> MemTable -> Flush to SSTable.
    - **Read Path**: Check MemTable -> Check Bloom Filter -> Search SSTables.
-  **Consensus**:
    - Manage Shard metadata/assignment via Raft (or existing CP store like etcd/ZooKeeper).
    - Replicate data within shard groups using Raft.

## 5. Next Steps
-  **Prototype Storage Engine**: Build a basic LSM-tree (Put, Get, Scan, Flush, Compact).
-  **Prototype WAL**: Ensure durability.
-  **Consensus Layer**: Integrate a Go Raft library (e.g., hashicorp/raft or etcd/raft) for replication.
