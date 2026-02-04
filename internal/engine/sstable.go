package engine

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// SSTable represents an immutable sorted string table on disk.
type SSTable struct {
	file          *os.File
	index         []IndexEntry
	DataEndOffset int64
}

type IndexEntry struct {
	Key    string
	Offset int64
}

// SSTableBuilder allows incremental construction of an SSTable.
type SSTableBuilder struct {
	file       *os.File
	index      []IndexEntry
	offset     int64
	entryCount int
}

// NewSSTableBuilder creates a new builder.
func NewSSTableBuilder(path string) (*SSTableBuilder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &SSTableBuilder{
		file: f,
	}, nil
}

// Add appends a key-value pair to the SSTable.
// Keys must be added in sorted order.
func (b *SSTableBuilder) Add(key string, value []byte) error {
	// Add to index (sparse)
	if b.entryCount%100 == 0 {
		b.index = append(b.index, IndexEntry{Key: key, Offset: b.offset})
	}

	// Write KeyLen (4), Key, ValueLen (4), Value
	kLen := int32(len(key))
	vLen := int32(len(value))

	if err := binary.Write(b.file, binary.LittleEndian, kLen); err != nil {
		return err
	}
	if _, err := b.file.WriteString(key); err != nil {
		return err
	}
	if err := binary.Write(b.file, binary.LittleEndian, vLen); err != nil {
		return err
	}
	if _, err := b.file.Write(value); err != nil {
		return err
	}

	b.offset += 4 + int64(kLen) + 4 + int64(vLen)
	b.entryCount++
	return nil
}

// Close finishes the SSTable (writes index and footer) and closes the file.
func (b *SSTableBuilder) Close() error {
	defer b.file.Close()

	// Write Index offset
	indexOffset := b.offset

	// Write Index
	for _, idx := range b.index {
		kLen := int32(len(idx.Key))
		if err := binary.Write(b.file, binary.LittleEndian, kLen); err != nil {
			return err
		}
		if _, err := b.file.WriteString(idx.Key); err != nil {
			return err
		}
		if err := binary.Write(b.file, binary.LittleEndian, idx.Offset); err != nil {
			return err
		}
	}

	// Write Footer: IndexOffset (8 bytes)
	if err := binary.Write(b.file, binary.LittleEndian, indexOffset); err != nil {
		return err
	}

	return nil
}

// WriteSSTable writes a MemTable to disk as an SSTable.
func WriteSSTable(mem *MemTable, path string) error {
	builder, err := NewSSTableBuilder(path)
	if err != nil {
		return err
	}

	// Get all entries sorted
	entries, _ := mem.All()
	for _, entry := range entries {
		if err := builder.Add(entry.Key, entry.Value); err != nil {
			builder.file.Close() // Force close on error
			return err
		}
	}

	return builder.Close()
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
		file:          f,
		index:         index,
		DataEndOffset: indexOffset,
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

// ScanKeys returns all keys in the range [start, end) without loading values.
// This is more efficient than Scan when only keys are needed.
func (t *SSTable) ScanKeys(start, end string) ([]string, error) {
	var keys []string

	// 1. Find start offset
	startOffset := int64(0)
	for _, idx := range t.index {
		if idx.Key <= start {
			startOffset = idx.Offset
		} else if idx.Key > start {
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

		// Skip the value by seeking forward
		if vLen > 0 {
			if _, err := t.file.Seek(int64(vLen), io.SeekCurrent); err != nil {
				return nil, err
			}
		}

		if currentKey >= end {
			break
		}

		if currentKey >= start {
			keys = append(keys, currentKey)
		}
	}

	return keys, nil
}

// SSTableIterator iterates over an SSTable.
type SSTableIterator struct {
	file   *os.File
	limit  int64 // Offset where data ends (start of index)
	offset int64
	k      string
	v      []byte
	err    error
	valid  bool
}

// NewIterator creates an iterator starting at the beginning of the data.
func (t *SSTable) NewIterator() (*SSTableIterator, error) {
	if _, err := t.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return &SSTableIterator{
		file:  t.file,
		limit: t.DataEndOffset,
	}, nil
}

func (it *SSTableIterator) Next() bool {
	it.valid = false // Assume invalid until success
	if it.offset >= it.limit {
		return false
	}

	var kLen int32
	err := binary.Read(it.file, binary.LittleEndian, &kLen)
	if err == io.EOF {
		return false
	}
	if err != nil {
		it.err = err
		return false
	}

	// Sanity check
	if kLen < 0 || kLen > 1024*1024 {
		// Could be index start
		it.err = fmt.Errorf("invalid key len: %d (end of data?)", kLen)
		return false
	}

	keyBuf := make([]byte, kLen)
	if _, err := io.ReadFull(it.file, keyBuf); err != nil {
		it.err = err
		return false
	}
	it.k = string(keyBuf)

	var vLen int32
	if err := binary.Read(it.file, binary.LittleEndian, &vLen); err != nil {
		it.err = err
		return false
	}

	if vLen < 0 || vLen > 16*1024*1024 { // 16MB limit for safety
		it.err = fmt.Errorf("invalid value len: %d", vLen)
		return false
	}

	valBuf := make([]byte, vLen)
	if _, err := io.ReadFull(it.file, valBuf); err != nil {
		it.err = err
		return false
	}
	it.v = valBuf

	// Update offset: 4(klen) + klen + 4(vlen) + vlen
	it.offset += 4 + int64(kLen) + 4 + int64(vLen)
	it.valid = true
	return true
}

func (it *SSTableIterator) Valid() bool {
	return it.valid
}

func (it *SSTableIterator) Key() string {
	return it.k
}

func (it *SSTableIterator) Value() []byte {
	return it.v
}

func (it *SSTableIterator) Error() error {
	if it.err == io.EOF {
		return nil
	}
	// We treat "invalid key len" as EOF if we hit index block?
	// Actually, index block starts immediately after data.
	// The index block structure is: KLen (4), Key, Offset (8).
	// Data block structure is: KLen (4), Key, VLen (4), Value.
	// They look similar! We need to detect when we hit index.
	// SSTable has a Footer at end saying where Index starts.
	// So `NewIterator` should take a limit?
	// Or `SSTable` struct knows `indexOffset`?
	// Wait, `SSTable` struct has `index` slice but not `indexOffset` stored.
	// But `OpenSSTable` calculates it. We should store it.
	return it.err
}

func (t *SSTable) Close() error {
	return t.file.Close()
}
