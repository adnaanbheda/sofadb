package engine

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"time"
	"strings"
	"sync"
)

const (
    // Flush threshold for MemTable (e.g., 4MB)
    MemTableSizeLimit = 4 * 1024 * 1024 
    // Compaction threshold: if more than 5 SSTables, compact them
    CompactionThreshold = 5
)

// LSM handles the orchestration of MemTable and SSTables.
type LSM struct {
	mu          sync.RWMutex
	dataDir     string
	mem         *MemTable
	immutable   []*MemTable // Queue of memtables being flushed
    ssTables    []*SSTable  // Ordered from newest to oldest
    walFile     *os.File    // Current Write-Ahead Log
}

// NewLSM creates a new LSM engine.
func NewLSM(dataDir string) (*LSM, error) {
    if err := os.MkdirAll(dataDir, 0755); err != nil {
        return nil, err
    }

    lsm := &LSM{
        dataDir: dataDir,
        mem:     NewMemTable(),
    }

    // Load existing SSTables
    if err := lsm.loadSSTables(); err != nil {
        return nil, err
    }
    
    // Recover from WAL
    if err := lsm.recoverFromWAL(); err != nil {
        return nil, err
    }

    return lsm, nil
}

func (l *LSM) recoverFromWAL() error {
    walPath := filepath.Join(l.dataDir, "memtable.wal")
    
    // Open WAL file
    f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE, 0644)
    if err != nil {
        return nil // Could be first run
    }
    l.walFile = f
    
    // Replay WAL
    // Format: KeyLen(4), ValueLen(4), Key, Value
    // ValueLen -1 for delete
    
    if _, err := f.Seek(0, 0); err != nil {
        return err
    }
    
    for {
        var kLen int32
        var vLen int32
        
        // Read header
        err := binary.Read(f, binary.LittleEndian, &kLen)
        if err == io.EOF {
            break
        }
        if err != nil {
            return err // corrupted?
        }
        if err := binary.Read(f, binary.LittleEndian, &vLen); err != nil {
            return err
        }
        
        // Read Key
        keyBuf := make([]byte, kLen)
        if _, err := io.ReadFull(f, keyBuf); err != nil {
            return err
        }
        key := string(keyBuf)
        
        // Read Value
        var value []byte
        if vLen >= 0 {
            value = make([]byte, vLen)
            if _, err := io.ReadFull(f, value); err != nil {
                return err
            }
            l.mem.Put(key, value)
        } else {
            l.mem.Put(key, nil) // Tombstone
        }
    }
    
    return nil
}

func (l *LSM) loadSSTables() error {
    files, err := ioutil.ReadDir(l.dataDir)
    if err != nil {
        return err
    }

    // Filter for .sst files
    var sstFiles []string
    for _, f := range files {
        if strings.HasSuffix(f.Name(), ".sst") {
            sstFiles = append(sstFiles, filepath.Join(l.dataDir, f.Name()))
        }
    }

    // Sort to ensure deterministic order (assuming naming convention or just load all)
    // For a real system, we need level manifest. For simplicity now, we rely on filename sort?
    // Actually, we sort reverse alphabetical assuming timestamps in names, so newest first.
    sort.Sort(sort.Reverse(sort.StringSlice(sstFiles)))

    for _, path := range sstFiles {
        table, err := OpenSSTable(path)
        if err != nil {
            return fmt.Errorf("failed to open sstable %s: %w", path, err)
        }
        l.ssTables = append(l.ssTables, table)
    }
    return nil
}

// Delete removes a key by inserting a tombstone.
func (l *LSM) Delete(key string) error {
	return l.Put(key, nil)
}

// Put writes to the MemTable. checks for flush.
func (l *LSM) Put(key string, value []byte) error {
    l.mu.Lock()
    defer l.mu.Unlock()

    // 1. Write to WAL
    if l.walFile != nil {
        kLen := int32(len(key))
        vLen := int32(-1) // Tombstone default
        if value != nil {
            vLen = int32(len(value))
        }
        
        if err := binary.Write(l.walFile, binary.LittleEndian, kLen); err != nil { return err }
        if err := binary.Write(l.walFile, binary.LittleEndian, vLen); err != nil { return err }
        if _, err := l.walFile.WriteString(key); err != nil { return err }
        if value != nil {
            if _, err := l.walFile.Write(value); err != nil { return err }
        }
        // In strictly durable systems, we'd Sync() here. For performance, maybe batch or OS buffer.
        // l.walFile.Sync() 
    }

    // 2. Write to MemTable
    l.mem.Put(key, value)

    if l.mem.Size() >= MemTableSizeLimit {
        return l.rotateMemTable()
    }
    return nil
}

// Get reads from MemTable, then Immutable MemTables, then SSTables.
func (l *LSM) Get(key string) ([]byte, error) {
    l.mu.RLock()
    defer l.mu.RUnlock()

    // 1. Check active MemTable
    if val, found := l.mem.Get(key); found {
		if val == nil {
			return nil, ErrKeyNotFound
		}
        return val, nil
    }

    // 2. Check immutable memtables
    for i := len(l.immutable) - 1; i >= 0; i-- {
        if val, found := l.immutable[i].Get(key); found {
			if val == nil {
				return nil, ErrKeyNotFound
			}
            return val, nil
        }
    }

    // 3. Check SSTables (Newest to Oldest)
    for _, sst := range l.ssTables {
        val, found, err := sst.Get(key)
        if err != nil {
            return nil, err
        }
        if found {
			if val == nil {
				return nil, ErrKeyNotFound
			}
            return val, nil
        }
    }

    return nil, ErrKeyNotFound
}

// ForceFlush forces the current MemTable to be flushed to an SSTable.
func (l *LSM) ForceFlush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotateMemTable()
}

// rotateMemTable moves current MemTable to immutable and triggers flush.
// Handling this synchronously for simplicity in V1.
func (l *LSM) rotateMemTable() error {
    oldMem := l.mem
    l.mem = NewMemTable()
    
    // In a real system, we'd add to l.immutable and flush async.
    // Here we flush sync to keep it simple and safe.
    
    filename := fmt.Sprintf("%d.sst", time.Now().UnixNano())
    path := filepath.Join(l.dataDir, filename)
    
    if err := WriteSSTable(oldMem, path); err != nil {
        return err
    }
    
    // Open the newly created table
    table, err := OpenSSTable(path)
    if err != nil {
        return err
    }
    
    // Add to SSTables list (at the front, as it is newest)
    l.ssTables = append([]*SSTable{table}, l.ssTables...)
    
    // Rotate WAL: Close old, Delete old, Open new
    if l.walFile != nil {
        l.walFile.Close()
    }
    walPath := filepath.Join(l.dataDir, "memtable.wal")
    os.Remove(walPath) // Data is now in SSTable
    
    f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE, 0644)
    if err != nil {
        return err
    }
    l.walFile = f
    
    // Check for compaction
    if len(l.ssTables) > CompactionThreshold {
        go l.Compact()
    }

    return nil
}

// Compact merges all SSTables into one.
// Simplified "Major Compaction" for now.
func (l *LSM) Compact() error {
    l.mu.Lock()
    defer l.mu.Unlock()
    
    // Basic check again under lock
    if len(l.ssTables) <= 1 {
        return nil
    }

    // 1. Scan all data from all SSTables
    // We reuse RangeScan logic but for the entire range
    // Since RangeScan uses LWW from l.ssTables, it correctly handles duplicates/deletes logic
    // EXCEPT RangeScan (v1) filtered tombstones out. 
    // For Compaction, we need to decide: do we keep tombstones?
    // If we are strictly merging ALL tables including oldest, we can drop tombstones 
    // because there is no older data to shadow. 
    // So yes, scanning and filtering tombstones is correct for a "Full Compaction".
    
    // However, RangeScan returns sorted struct. We can iterate that.
    
    kvs, err := l.RangeScan("", "\xFF") // Scan everything
    if err != nil {
        return err
    }
    
    // 2. Write to new SSTable
    filename := fmt.Sprintf("%d.sst", time.Now().UnixNano())
    path := filepath.Join(l.dataDir, filename)
    
    // Create temporary MemTable to reuse WriteSSTable? 
    // No, MemTable might be too big for RAM if DB is huge.
    // But our WriteSSTable takes *MemTable.
    // Ideally we should have a `SSTableBuilder` that takes stream of keys.
    // For V1, let's assume compaction result fits in RAM (simplicity trade-off).
    // Or refactor WriteSSTable.
    
    // Let's refactor WriteSSTable slightly or just construct a MemTable.
    // If we assume MemTableSizeLimit is small, then compacting huge data into one 
    // MemTable to write it violates design.
    // WE NEED STREMING WRITE.
    
    // Let's implement a simple streaming writer here inline for now.
    
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer f.Close()

	var index []IndexEntry
	offset := int64(0)

	for i, kv := range kvs {
		if i%100 == 0 {
			index = append(index, IndexEntry{Key: kv.Key, Offset: offset})
		}
		
		kLen := int32(len(kv.Key))
		vLen := int32(len(kv.Value))

		if err := binary.Write(f, binary.LittleEndian, kLen); err != nil { return err }
		if _, err := f.WriteString(kv.Key); err != nil { return err }
		if err := binary.Write(f, binary.LittleEndian, vLen); err != nil { return err }
		if _, err := f.Write(kv.Value); err != nil { return err }

		offset += 4 + int64(kLen) + 4 + int64(vLen)
	}
    
    // Write Index
    indexOffset := offset
	for _, idx := range index {
		kLen := int32(len(idx.Key))
		if err := binary.Write(f, binary.LittleEndian, kLen); err != nil { return err }
		if _, err := f.WriteString(idx.Key); err != nil { return err }
		if err := binary.Write(f, binary.LittleEndian, idx.Offset); err != nil { return err }
	}
	if err := binary.Write(f, binary.LittleEndian, indexOffset); err != nil { return err }

    // 3. Close new SSTable
    f.Close() // Explicit close before reopen
    
    newTable, err := OpenSSTable(path)
    if err != nil {
        return err
    }
    
    // 4. Update LSM state (Replace all old tables with new one)
    oldTables := l.ssTables
    l.ssTables = []*SSTable{newTable}
    
    // 5. Delete old files
    // We need to be careful with file locks on Windows if readers are active.
    // We are under mu.Lock() so no new method calls start, but ongoing reads?
    // We used RLock for reads. So Lock() blocks until readers finish.
    // This is safe.
    
    for _, t := range oldTables {
        t.Close()
        os.Remove(t.file.Name())
    }
    
    return nil
}

// RangeScan returns values for keys in [start, end).
func (l *LSM) RangeScan(start, end string) ([]struct {
	Key   string
	Value []byte
}, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// data map to handle LWW (Last Write Wins)
	data := make(map[string][]byte)

	// 1. Scan SSTables (Oldest to Newest)
	for i := len(l.ssTables) - 1; i >= 0; i-- {
		sst := l.ssTables[i]
		rows, err := sst.Scan(start, end)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			data[row.Key] = row.Value
		}
	}

	// 2. Scan MemTable
	memEntries, _ := l.mem.All()
	for _, entry := range memEntries {
		if entry.Key >= start && entry.Key < end {
			data[entry.Key] = entry.Value
		}
	}

    // 3. Convert map to sorted slice AND filter tombstones
    var sortedKeys []string
    for k, v := range data {
		if v != nil { // Only add if not a tombstone
        	sortedKeys = append(sortedKeys, k)
		}
    }
    sort.Strings(sortedKeys)

    var result []struct {
        Key   string
        Value []byte
    }
    for _, k := range sortedKeys {
        result = append(result, struct{
            Key   string
            Value []byte
        }{k, data[k]})
    }

	return result, nil
}

func (l *LSM) Close() error {
    l.mu.Lock()
    defer l.mu.Unlock()
    for _, sst := range l.ssTables {
        sst.Close()
    }
    return nil
}
