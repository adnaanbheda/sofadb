# Paxos Made Simple

## Overview
This paper by Leslie Lamport provides a simplified, plain-English explanation of the Paxos algorithm, stripping away the Greek mythology formalism of the original paper. It focuses on the consensus algorithm and its application to implementing a distributed state machine.

## The Consensus Algorithm
The goal is to choose a single value from a set of proposed values. Agents are: **Proposers**, **Acceptors**, and **Learners**.

### Safety Requirements
- Only a proposed value may be chosen.
- Only a single value is chosen.
- A process never learns a value has been chosen unless it actually has been.

### The Algorithm (Basic Paxos)
**Phase 1: Prepare**
1.  **Proposer**: Selects a proposal number $n$ and sends a `Prepare(n)` request to a majority of acceptors.
2.  **Acceptor**: If $n >$ any prepare request already responded to:
    - Responds with a promise not to accept any more proposals numbered $< n$.
    - Returns the highest-numbered proposal (if any) that it has accepted so far.

**Phase 2: Accept**
1.  **Proposer**: If it receives responses from a majority:
    - Creates a proposal with number $n$ and value $v$.
    - $v$ = value of the highest-numbered proposal among the responses, OR any value if no proposals were reported.
    - Sends `Accept(n, v)` request to those acceptors.
2.  **Acceptor**: If it receives `Accept(n, v)`, it accepts the proposal unless it has already responded to a prepare request $> n$.

**Phase 3: Learn**
- To learn a chosen value, a learner must find out that a proposal has been accepted by a majority.
- Acceptors can inform a distinguished learner (or set of learners) upon acceptance.

## Liveness (Progress)
- **Problem**: Two proposers can get into a livelock (dueling proposers), continuously issuing higher prepare requests (Phase 1) preventing each other from completing Phase 2.
- **Solution**: Elect a **Distinguished Proposer** (Leader). If only one proposer is working, the algorithm terminates.

## Implementing a State Machine
- Use a sequence of Paxos instances, one for each state machine command (log entry).
- The $i$-th instance chooses the $i$-th command.
- **Leader Optimization**: A stable leader can execute Phase 1 for *all* future instances at once (just sending a standard "I am leader" message). Then basic consensus becomes just one round trip (Phase 2).

## Relevance to SofaDB
- **Fundamental Theory**: This provides the clear, no-nonsense derivation of why distributed consensus involves two phases (Propose/Promise, Accept/Commit).
- **Implementation**: Highlights the role of a Leader for performance and liveness, which matches Raft and other practical systems.
