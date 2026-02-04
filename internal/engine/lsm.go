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

type Iterator interface {
	Next() bool
	Valid() bool
	Key() string
	Value() []byte
	Error() error
}

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
        if err == io.ErrUnexpectedEOF {
             // Partial write at end of WAL (crash). Truncate and continue.
             // We need to truncate the file to the current offset, but here we just stop.
             // The file is opened O_RDWR, so next writes append. We should probably Truncate to clean up.
             currentPos, _ := f.Seek(0, io.SeekCurrent)
             f.Truncate(currentPos - int64(4)) // Rewind the 4 bytes we tried to read? No, Read failed. 
             // Actually, if Read failed, the file pointer might have moved or not depending on implementation.
             // Safer to just Log and Ignore for now, relying on next compaction to clean up.
             fmt.Println("Recovered partial WAL entry (truncated)")
             break
        }
        if err != nil {
            return err // Real IO error
        }

        if err := binary.Read(f, binary.LittleEndian, &vLen); err != nil {
             if err == io.EOF || err == io.ErrUnexpectedEOF {
                 fmt.Println("Recovered partial WAL entry (truncated at vLen)")
                 break
             }
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

// Compact merges all SSTables into one using streaming.
func (l *LSM) Compact() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Basic check again under lock
	if len(l.ssTables) <= 1 {
		return nil
	}

	// 1. Create Iterators for all SSTables
	var iterators []Iterator
	for _, sst := range l.ssTables {
		it, err := sst.NewIterator()
		if err != nil {
			return err
		}
		iterators = append(iterators, it)
	}

	mergedIter := NewMergedIterator(iterators)

	// 2. Stream to new SSTable
	filename := fmt.Sprintf("%d.sst", time.Now().UnixNano())
	path := filepath.Join(l.dataDir, filename)

	builder, err := NewSSTableBuilder(path)
	if err != nil {
		return err
	}
	defer builder.file.Close() // Safety close

	for mergedIter.Next() {
		// Filter tombstones (nil value)
		// For a full compaction (Major), we can discard tombstones if we are sure no older data exists.
		// Since we merge ALL overlapping tables (level 0), and there is no Level 1+, 
		// we are effectively doing a Full Compaction.
		// So yes, drop tombstones.
		if mergedIter.Value() == nil {
			continue
		}
		
		if err := builder.Add(mergedIter.Key(), mergedIter.Value()); err != nil {
			return err
		}
	}

	if err := mergedIter.Error(); err != nil {
		return err
	}

	if err := builder.Close(); err != nil {
		return err
	}

	// 3. Open new SSTable
	newTable, err := OpenSSTable(path)
	if err != nil {
		return err
	}

	// 4. Update LSM state
	oldTables := l.ssTables
	l.ssTables = []*SSTable{newTable}

	// 5. Delete old files
	for _, t := range oldTables {
		t.Close()
		os.Remove(t.file.Name())
	}

	return nil
}

// MergedIterator performs an N-way merge of iterators.
type MergedIterator struct {
	iters []Iterator
	currK string
	currV []byte
	err   error
}

func NewMergedIterator(iters []Iterator) *MergedIterator {
    // Prime all iterators
    for _, it := range iters {
        it.Next() 
    }
	return &MergedIterator{iters: iters}
}

func (m *MergedIterator) Next() bool {
	// 1. Find the smallest key among all valid iterators
	smallestKey := ""
	first := true

	// Check errors first
	for _, it := range m.iters {
		if it.Error() != nil {
			m.err = it.Error()
			return false
		}
	}

	// Scan for min key
	for _, it := range m.iters {
		if !it.Valid() {
			continue
		}
		if first || it.Key() < smallestKey {
			smallestKey = it.Key()
			first = false
		}
	}

	if first {
		// No valid iterators left
		return false
	}

	m.currK = smallestKey
	m.currV = nil
	foundValue := false

	// 2. Advance all iterators that have this key
	// Since m.iters is ordered Newest -> Oldest, the first one we find 
	// is the one we keep. We discard the rest (shadowed).
	for _, it := range m.iters {
		if it.Valid() && it.Key() == smallestKey {
			if !foundValue {
				m.currV = it.Value()
				foundValue = true
			}
			// Build internal state for next call
			it.Next()
		}
	}

	return true
}


func (m *MergedIterator) Key() string   { return m.currK }
func (m *MergedIterator) Value() []byte { return m.currV }
func (m *MergedIterator) Error() error  { return m.err }

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
    
    // Flush current MemTable if it has data
    if l.mem.Size() > 0 {
		// We can reuse rotateMemTable logic but we need to be careful not to trigger async compaction 
		// if we are shutting down. But rotateMemTable triggers it in goroutine.
		// That might be fine, the process will exit and kill the goroutine.
		// Or we can just call the flush logic directly.
		// Let's call rotateMemTable for consistency.
		if err := l.rotateMemTable(); err != nil {
			return err
		}
    }
    
    for _, sst := range l.ssTables {
        sst.Close()
    }
    return nil
}
