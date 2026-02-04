# Chaos Engineering Plan

To certify SofaDB as "Production Grade," we must verify its durability guarantees under failure conditions.

## Objectives
1.  **Durability**: Committed writes (acknowledged Put) must not be lost after a hard crash.
2.  **Recovery**: The system must successfully restart after a crash, even with corrupted/partial log files.
3.  **Consistency**: Replayed data must exactly match the state before the crash.

## Scenarios

### 1. Partial WAL Write (The "Power Plug" Test)
*   **Trigger**: Write to WAL, then `kill -9` (or `panic`) before `Sync` or before full entry is written.
*   **Expected Behavior**: 
    *   On restart, `recoverFromWAL` should detect the `unexpected EOF`.
    *   It should truncate the corrupted entry.
    *   It should successfully load all valid prior entries.
    *   Server should start up (not panic).

### 2. Compaction Interrupt
*   **Trigger**: Kill process while `Compact()` is merging SSTables.
*   **Expected Behavior**:
    *   Old SSTables should still be intact (atomic swap hasn't happened).
    *   Partially written new SSTable (`.sst` file) might exist.
    *   On restart, engine should ignore or delete the partial SSTable.
    *   Data remains consistent.

### 3. Rapid Restart Loop
*   **Trigger**: Start -> Write -> Kill -> Start -> Write -> Kill (Loop 100x).
*   **Expected Behavior**: No data corruption.

## Implementation Strategy (`internal/engine/chaos_test.go`)

We will use Go's `exec.Command` to spawn a "victim" process that runs the engine and crashes on command.

```go
// Psuedo-code for TestChaosWAL
func TestChaosWAL(t *testing.T) {
    // 1. Spawn child process that writes N keys and crashes
    cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
    cmd.Env = append(os.Environ(), "CRASH_AT_WAL_OFFSET=1024")
    cmd.Run() 
    
    // 2. Restart engine normally
    eng, _ := NewLSM(dir)
    
    // 3. Verify keys exist
}
```
