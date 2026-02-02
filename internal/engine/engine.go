// Package engine provides the storage engine for SofaDB.
// It implements an append-only log with an in-memory index for fast lookups.
package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	// MaxKeySize is the maximum allowed key size in bytes
	MaxKeySize = 256
	// DataFileName is the name of the data file
	DataFileName = "sofadb.data"
)

var (
	// ErrKeyNotFound is returned when a key doesn't exist
	ErrKeyNotFound = errors.New("key not found")
	// ErrKeyTooLong is returned when a key exceeds MaxKeySize
	ErrKeyTooLong = errors.New("key too long")
	// ErrEngineShutdown is returned when the engine is closed
	ErrEngineShutdown = errors.New("engine is shut down")
)

// Engine is the storage engine for SofaDB.
// It provides thread-safe access to a key-document store.
type Engine struct {
	mu       sync.RWMutex
	dataDir  string
	dataFile *os.File
	index    map[string]int64 // key -> file offset
	closed   bool
}

// entryHeader represents the header of a log entry
type entryHeader struct {
	KeyLen   uint32
	ValueLen int32 // -1 indicates a tombstone (deleted entry)
}

const headerSize = 8 // 4 bytes for key length + 4 bytes for value length

// New creates a new Engine with the given data directory.
// It creates the directory if it doesn't exist and replays the log to rebuild the index.
func New(dataDir string) (*Engine, error) {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dataPath := filepath.Join(dataDir, DataFileName)

	// Open or create the data file
	dataFile, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open data file: %w", err)
	}

	e := &Engine{
		dataDir:  dataDir,
		dataFile: dataFile,
		index:    make(map[string]int64),
	}

	// Rebuild index from existing data
	if err := e.rebuildIndex(); err != nil {
		dataFile.Close()
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}

	return e, nil
}

// rebuildIndex replays the log file to rebuild the in-memory index.
func (e *Engine) rebuildIndex() error {
	// Seek to beginning of file
	if _, err := e.dataFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	offset := int64(0)

	for {
		// Read header
		var header entryHeader
		if err := binary.Read(e.dataFile, binary.LittleEndian, &header); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read entry header at offset %d: %w", offset, err)
		}

		// Validate key length
		if header.KeyLen > MaxKeySize {
			return fmt.Errorf("invalid key length %d at offset %d", header.KeyLen, offset)
		}

		// Read key
		keyBuf := make([]byte, header.KeyLen)
		if _, err := io.ReadFull(e.dataFile, keyBuf); err != nil {
			return fmt.Errorf("failed to read key at offset %d: %w", offset, err)
		}
		key := string(keyBuf)

		if header.ValueLen < 0 {
			// Tombstone - remove from index
			delete(e.index, key)
		} else {
			// Store offset in index
			e.index[key] = offset

			// Skip value bytes
			if _, err := e.dataFile.Seek(int64(header.ValueLen), io.SeekCurrent); err != nil {
				return fmt.Errorf("failed to skip value at offset %d: %w", offset, err)
			}
		}

		// Calculate next offset
		entrySize := int64(headerSize) + int64(header.KeyLen)
		if header.ValueLen >= 0 {
			entrySize += int64(header.ValueLen)
		}
		offset += entrySize
	}

	return nil
}

// Get retrieves a document by key.
// Returns ErrKeyNotFound if the key doesn't exist.
func (e *Engine) Get(key string) ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.closed {
		return nil, ErrEngineShutdown
	}

	offset, ok := e.index[key]
	if !ok {
		return nil, ErrKeyNotFound
	}

	// Seek to the entry
	if _, err := e.dataFile.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to entry: %w", err)
	}

	// Read header
	var header entryHeader
	if err := binary.Read(e.dataFile, binary.LittleEndian, &header); err != nil {
		return nil, fmt.Errorf("failed to read entry header: %w", err)
	}

	// Skip key
	if _, err := e.dataFile.Seek(int64(header.KeyLen), io.SeekCurrent); err != nil {
		return nil, fmt.Errorf("failed to skip key: %w", err)
	}

	// Read value
	value := make([]byte, header.ValueLen)
	if _, err := io.ReadFull(e.dataFile, value); err != nil {
		return nil, fmt.Errorf("failed to read value: %w", err)
	}

	return value, nil
}

// Put stores a document with the given key.
// If the key already exists, the document is updated.
func (e *Engine) Put(key string, value []byte) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLong
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrEngineShutdown
	}

	// Seek to end of file
	offset, err := e.dataFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek to end: %w", err)
	}

	// Write header
	header := entryHeader{
		KeyLen:   uint32(len(key)),
		ValueLen: int32(len(value)),
	}
	if err := binary.Write(e.dataFile, binary.LittleEndian, &header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Write key
	if _, err := e.dataFile.WriteString(key); err != nil {
		return fmt.Errorf("failed to write key: %w", err)
	}

	// Write value
	if _, err := e.dataFile.Write(value); err != nil {
		return fmt.Errorf("failed to write value: %w", err)
	}

	// Sync to disk
	if err := e.dataFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync: %w", err)
	}

	// Update index
	e.index[key] = offset

	return nil
}

// Delete removes a document by key.
// Returns ErrKeyNotFound if the key doesn't exist.
func (e *Engine) Delete(key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrEngineShutdown
	}

	if _, ok := e.index[key]; !ok {
		return ErrKeyNotFound
	}

	// Seek to end of file
	if _, err := e.dataFile.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("failed to seek to end: %w", err)
	}

	// Write tombstone header
	header := entryHeader{
		KeyLen:   uint32(len(key)),
		ValueLen: -1, // Tombstone marker
	}
	if err := binary.Write(e.dataFile, binary.LittleEndian, &header); err != nil {
		return fmt.Errorf("failed to write tombstone header: %w", err)
	}

	// Write key
	if _, err := e.dataFile.WriteString(key); err != nil {
		return fmt.Errorf("failed to write key: %w", err)
	}

	// Sync to disk
	if err := e.dataFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync: %w", err)
	}

	// Remove from index
	delete(e.index, key)

	return nil
}

// Keys returns all keys in the store.
func (e *Engine) Keys() ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.closed {
		return nil, ErrEngineShutdown
	}

	keys := make([]string, 0, len(e.index))
	for k := range e.index {
		keys = append(keys, k)
	}
	return keys, nil
}

// Count returns the number of documents in the store.
func (e *Engine) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.index)
}

// Close closes the engine and releases resources.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	e.closed = true
	return e.dataFile.Close()
}
