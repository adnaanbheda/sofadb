# The Part-Time Parliament (Paxos)

## Overview
This paper by Leslie Lamport introduces the **Paxos** algorithm through an allegory of a Greek island's parliament. The problem is how to reach consensus (pass decrees) in a system where legislators (processors) are unreliable, peripatetic (can leave/fail), and communicate via messengers (messages) that can be delayed or lost, but not corrupted.

## Core Problem
- **Consensus**: Ensuring all processes agree on a single value (decree).
- **Fault Tolerance**: The system must function as long as a majority of processes are available.
- **Safety properties**:
    - Only a value that has been proposed may be chosen.
    - Only a single value is chosen.
    - A process never learns that a value has been chosen unless it actually has been.

## The Single-Decree Synod Protocol
The core algorithm describes how to agree on a single decree.
1.  **Ballots**: Changes are proposed in numbered ballots. Ballot numbers must be unique and totally ordered.
2.  **Quorums**: A ballot succeeds if a majority (quorum) of priests vote for it.
3.  **Consistency Conditions**:
    - **B1**: Each ballot has a unique number.
    - **B2**: Any two quorums must intersect (have at least one common member).
    - **B3**: If a vote is cast in a ballot, it must respect decisions from earlier ballots.

### Protocol Phases (Basic)
1.  **Prepare (NextBallot)**: A proposer chooses a ballot number $b$ and sends valid messages to a quorum.
2.  **Promise (LastVote)**: Legislators respond. If $b$ is higher than any ballot they've voted in, they promise not to vote in ballots $<b$ and send back their highest-numbered previous vote ($v$, if any).
3.  **Propose (BeginBallot)**: If proposer receives promises from a quorum:
    - If any legislator returned a previous vote, the proposer **must** adopt the value from the highest-numbered ballot returned.
    - If no previous votes returned, proposer can choose any value.
    - Sends proposal with ballot $b$ and value $v$.
4.  **Accept (Voted)**: Legislators vote for the proposal if they haven't promised to ignore ballot $b$.
5.  **Learn (Success)**: Once a value is chosen (accepted by majority), it is communicated.

## Multi-Decree Parliament
To implement a distributed state machine (like a database log), multiple decrees are passed (numbered 1, 2, 3...).
- **President**: To avoid liveness issues (dueling proposers), a distinguished leader (President) is elected.
- **Optimization**: The leader runs the first phase (Prepare/Promise) once for all future instances, allowing for a 1-round trip consensus for subsequent commands.

## Relevance to SofaDB
- **Distributed Consensus**: Critical for replication and leader election.
- **State Machine Replication**: The fundamental model for building consistent distributed databases (like transforming a log of commands into a database state).
- **Understanding**: This paper gives the theoretical foundation, though "Paxos Made Simple" is often easier to implementation details from.
