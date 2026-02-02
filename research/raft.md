# Raft: In Search of an Understandable Consensus Algorithm

## Overview
Raft is a consensus algorithm derived to be equivalent to Paxos in fault tolerance and performance, but designed specifically to be **understandable**. It manages a replicated log for a replicated state machine.

## Design for Understandability
- **Decomposition**: Separation of key elements: Leader Election, Log Replication, Safety, and Membership Changes.
- **State Space Reduction**: Reduces nondeterminism (e.g., strong leader, logs don't have holes).

## Core Algorithm Components

### 1. Leader Election
- **Heartbeats**: Leaders send periodic heartbeats. If a follower receives no communication for an *election timeout* (randomized, e.g., 150-300ms), it starts an election.
- **Term**: Time divided into numbered terms. Terms act as a logical clock.
- **Voting**: 
    - Candidate increments term, votes for self, sends `RequestVote` RPCs.
    - Follower votes for first valid candidate in a term (if candidate's log is at least as up-to-date).
    - Majority winner becomes Leader.

### 2. Log Replication
- **Strong Leader**: Only the leader accepts client entries. Entries flow Leader $\to$ Followers.
- **AppendEntries RPC**: Leader sends log entries to followers.
- **Consistency Check**: Optimistically assumes followers' logs match. If not, it backtracks to find the last common point and overwrites follower's conflicting entries.
- **Commitment**: An entry is committed once replicated to a majority. Leader tracks `commitIndex`.

### 3. Safety
- **Election Restriction**: A candidate cannot win election if its log doesn't contain all committed entries (checked via log term/index comparison during voting).
- **Leader Completeness**: If log entry committed in term $T$, it is present in leaders of all terms $> T$.
- **State Machine Safety**: If a server applies entry at index $i$, no other server will apply a different entry for $i$.

## Cluster Membership
- **Joint Consensus**: Two-phase approach to change configuration (e.g., 3 nodes to 5 nodes) safely without stopping the cluster.
    - Phase 1: Switch to "Joint Consensus" (requires majorities of both old and new configurations).
    - Phase 2: Switch to "New Configuration".

## Relevance to SofaDB
- **Consensus Choice**: Raft is generally preferred over Paxos for new implementations due to its understandability and complete specification (including cluster membership).
- **Log Replication**: The log structure and replication mechanism are directly applicable to a distributed database like SofaDB.
- **Leader-based**: Matches the architecture of having a coordinator/leader for partitions.
