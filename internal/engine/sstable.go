package engine

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// SSTable represents an immutable sorted string table on disk.
type SSTable struct {
	file  *os.File
	index []IndexEntry
}

type IndexEntry struct {
	Key    string
	Offset int64
}

// WriteSSTable writes a MemTable to disk as an SSTable.
func WriteSSTable(mem *MemTable, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Get all entries sorted
	entries, _ := mem.All() // MemTable.All returns sorted entries from skip list

	var index []IndexEntry
	offset := int64(0)

	// Write data
	for i, entry := range entries {
		// Add to index every 100 entries (sparse index) or if it's the first
		if i%100 == 0 {
			index = append(index, IndexEntry{Key: entry.Key, Offset: offset})
		}

		// Write KeyLen (4), Key, ValueLen (4), Value
		kLen := int32(len(entry.Key))
		vLen := int32(len(entry.Value))

		if err := binary.Write(f, binary.LittleEndian, kLen); err != nil {
			return err
		}
		if _, err := f.WriteString(entry.Key); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, vLen); err != nil {
			return err
		}
		if _, err := f.Write(entry.Value); err != nil {
			return err
		}

		offset += 4 + int64(kLen) + 4 + int64(vLen)
	}

	// Write Index offset
	indexOffset := offset

	// Write Index
	for _, idx := range index {
		kLen := int32(len(idx.Key))
		if err := binary.Write(f, binary.LittleEndian, kLen); err != nil {
			return err
		}
		if _, err := f.WriteString(idx.Key); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, idx.Offset); err != nil {
			return err
		}
	}

	// Write Footer: IndexOffset (8 bytes)
	if err := binary.Write(f, binary.LittleEndian, indexOffset); err != nil {
		return err
	}

	return nil
}

// OpenSSTable opens an existing SSTable.
func OpenSSTable(path string) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	// Read footer (last 8 bytes)
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := stat.Size()
	if size < 8 {
		return nil, fmt.Errorf("invalid sstable size")
	}

	if _, err := f.Seek(size-8, io.SeekStart); err != nil {
		return nil, err
	}

	var indexOffset int64
	if err := binary.Read(f, binary.LittleEndian, &indexOffset); err != nil {
		return nil, err
	}

	// Read Index
	if _, err := f.Seek(indexOffset, io.SeekStart); err != nil {
		return nil, err
	}

	var index []IndexEntry
	// Read until we hit the footer offset (size-8)
	currentPos := indexOffset
	for currentPos < size-8 {
		var kLen int32
		if err := binary.Read(f, binary.LittleEndian, &kLen); err != nil {
			return nil, err
		}
		
		keyBuf := make([]byte, kLen)
		if _, err := io.ReadFull(f, keyBuf); err != nil {
			return nil, err
		}
		
		var offset int64
		if err := binary.Read(f, binary.LittleEndian, &offset); err != nil {
			return nil, err
		}

		index = append(index, IndexEntry{Key: string(keyBuf), Offset: offset})
		currentPos += 4 + int64(kLen) + 8
	}

	return &SSTable{
		file:  f,
		index: index,
	}, nil
}

// Get searches for a key in the SSTable.
func (t *SSTable) Get(key string) ([]byte, bool, error) {
	// 1. Check index to find start offset
	// Find the largest key in index <= key
	startOffset := int64(0)
	for _, idx := range t.index {
		if idx.Key <= key {
			startOffset = idx.Offset
		} else {
			break
		}
	}

	// 2. Scan from offset
	if _, err := t.file.Seek(startOffset, io.SeekStart); err != nil {
		return nil, false, err
	}

	for {
		var kLen int32
		err := binary.Read(t.file, binary.LittleEndian, &kLen)
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}

		// Safety check for huge keys or corrupt file reading into data
		if kLen > 1024*1024 || kLen < 0 { 
			// We might have read into the index part if we scanned too far?
			// Or just bad data.
			return nil, false, nil
		}

		keyBuf := make([]byte, kLen)
		if _, err := io.ReadFull(t.file, keyBuf); err != nil {
			return nil, false, err
		}
		currentKey := string(keyBuf)

		var vLen int32
		if err := binary.Read(t.file, binary.LittleEndian, &vLen); err != nil {
			return nil, false, err
		}
		
		valBuf := make([]byte, vLen)
		if _, err := io.ReadFull(t.file, valBuf); err != nil {
			return nil, false, err
		}

		if currentKey == key {
			return valBuf, true, nil
		}
		if currentKey > key {
			// passed it
			return nil, false, nil
		}
	}
}

// Scan returns all key-value pairs in the range [start, end).
func (t *SSTable) Scan(start, end string) ([]struct {
	Key   string
	Value []byte
}, error) {
	var results []struct {
		Key   string
		Value []byte
	}

	// 1. Find start offset
	startOffset := int64(0)
	for _, idx := range t.index {
		if idx.Key <= start {
			startOffset = idx.Offset
		} else if idx.Key > start {
            // Optimization: if the index key itself is > start, we can't skip past it, 
            // but we might have undershot with the previous one. 
            // The logic "largest key <= start" is correct for sparse index.
			break
		}
	}

	// 2. Seek
	if _, err := t.file.Seek(startOffset, io.SeekStart); err != nil {
		return nil, err
	}

	// 3. Iterate
	for {
		var kLen int32
		err := binary.Read(t.file, binary.LittleEndian, &kLen)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

        // Sanity check
        if kLen < 0 || kLen > 1024*1024 {
             break // Potentially hit footer or garbage
        }

		keyBuf := make([]byte, kLen)
		if _, err := io.ReadFull(t.file, keyBuf); err != nil {
			return nil, err
		}
		currentKey := string(keyBuf)

		var vLen int32
		if err := binary.Read(t.file, binary.LittleEndian, &vLen); err != nil {
			return nil, err
		}
		
		valBuf := make([]byte, vLen)
		if _, err := io.ReadFull(t.file, valBuf); err != nil {
			return nil, err
		}

		if currentKey >= end {
			break
		}

		if currentKey >= start {
			results = append(results, struct {
				Key   string
				Value []byte
			}{currentKey, valBuf})
		}
	}

	return results, nil
}

func (t *SSTable) Close() error {
	return t.file.Close()
}
