# Bigtable: A Distributed Storage System for Structured Data

## Overview
Bigtable is a distributed storage system designed by Google to scale to petabytes of data across thousands of commodity servers. It is used for applications like Web Indexing, Google Earth, and Google Finance.

## Data Model
- **Structure**: Sparse, distributed, persistent multi-dimensional sorted map.
- **Indexing**: Row key, Column key, Timestamp.
- **Mapping**: `(row:string, column:string, time:int64) -> string`
- **Rows**: 
    - Arbitrary strings (typically 10-100 bytes).
    - Operations under a single row key are atomic.
    - Data maintained in lexicographic order by row key.
    - Ranges of rows are dynamically partitioned into **Tablets**.
- **Column Families**:
    - Groups of column keys.
    - Basic unit of access control and accounting.
    - Syntax: `family:qualifier`.
    - Data in a family is compressed together.
- **Timestamps**:
    - 64-bit integers (real time or explicit).
    - Versions stored in decreasing timestamp order.
    - GC configurable (keep last $n$ versions or last $n$ days).

## Architecture
- **Client Library**: Linked into every client; handles communication.
- **Master Server**: 
    - Assigns tablets to tablet servers.
    - Detects added/expired tablet servers.
    - Balances load.
    - Handles schema changes.
    - *Note*: Clients communicate directly with tablet servers for data, bypassing the master.
- **Tablet Servers**:
    - Manages a set of tablets (typically 10-1000).
    - Handles read/write requests.
    - Splits tablets when they grow too large.
- **Underlying Infrastructure**:
    - **GFS**: Stores log and data files (SSTables).
    - **Chubby**: Distributed lock service for master election, tablet location bootstrapping, failure detection, and schema/ACL storage.

## Implementation Details
- **SSTable**: Immutable, ordered map of key-value pairs stored in GFS.
- **Memtable**: In-memory sorted buffer for recent updates.
- **Write Flow**: Write to commit log (GFS) -> Insert into Memtable.
- **Read Flow**: Merge view of Memtable + sequence of SSTables.
- **Compactions**:
    - **Minor**: Memtable -> SSTable (reduces memory usage/recovery time).
    - **Merging**: Merges few SSTables + Memtable -> New SSTable.
    - **Major**: Rewrites all SSTables into one; reclaims space from deleted data.

## Optimizations
- **Locality Groups**: Group column families to be stored/compressed together (or in-memory).
- **Compression**: User-specified per locality group (e.g., Bentley-McIlroy + standard fast compression).
- **Caching**: 
    - **Scan Cache**: Key-value pairs.
    - **Block Cache**: SSTable blocks (from GFS).
- **Bloom Filters**: Reduce disk seeks for non-existent row/column lookups.

## Relevance to SofaDB
- **Data Model**: The Row/Column/Timestamp model is powerful for versioned data.
- **LSM-Tree approach**: Memtable + SSTables is the standard for high write throughput.
- **SSTable format**: Immutable files complicate updates but simplify concurrency and recovery.
- **Compression**: Column-family level compression is efficient for similar data types.
