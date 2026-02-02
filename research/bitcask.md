# Bitcask: A Log-Structured Hash Table for Fast Key/Value Data

## Overview
Bitcask is a local key/value storage system developed by Basho Technologies, originally for Riak. It is designed for low latency, high throughput, and handling datasets larger than RAM.

## Data Model & Format
- **Structure**: Log-structured hash table.
- **Directory**: A Bitcask instance is a directory.
- **Files**:
    - **Active File**: One file is active for writing (append-only).
    - **Older Files**: Immutable once closed.
- **Entry Format**: `[crc][tstamp][ksz][value_sz][key][value]`
- **Deletion**: Writing a tombstone value.

## In-Memory Index (Keydir)
- **Keydir**: A hash table mapping every key to its location on disk.
    - Structure: `Key -> {file_id, value_sz, value_pos, tstamp}`
- **Operations**:
    - **Write**: Append to active file -> Update Keydir.
    - **Read**: Look up Keydir -> Single disk seek to read value.
- **Startup**: Scans all data files to build Keydir (fast with hint files).

## Compaction (Merging)
- **Merge Process**: Iterates over immutable files, keeps only the latest version of each key, and writes to new merged files.
- **Hint Files**: Created during merge. Contains position/size instead of value. used to speed up startup (Keydir construction).

## Pros & Cons
- **Pros**:
    - Low latency (1 disk seek for read).
    - High write throughput (sequential append).
    - Crash friendly (no replay needed, data/log are same).
    - Simple backup/restore.
- **Cons**:
    - **Keydir must fit in RAM**: This is the main limitation. If keys don't fit in RAM, Bitcask is not suitable.
    - No compression (by default).

## Relevance to SofaDB
- **Simplicity**: Bitcask is extremely simple to implement compared to LSM-Trees (Bigtable).
- **Fast Writes**: Append-only nature matches the goal of high write speed.
- **Index constraint**: Requires all keys to fit in memory. If SofaDB targets datasets where keys > RAM, Bitcask might need modification or a different approach (like SSTables/LSM).
