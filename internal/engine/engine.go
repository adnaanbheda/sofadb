// Package engine provides the storage engine for SofaDB.
// It implements an LSM Tree storage engine.
package engine

import (
	"errors"
	"sync"
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
	mu     sync.RWMutex
	lsm    *LSM
	closed bool
}

// New creates a new Engine with the given data directory.
func New(dataDir string) (*Engine, error) {
	lsm, err := NewLSM(dataDir)
	if err != nil {
		return nil, err
	}

	return &Engine{
		lsm: lsm,
	}, nil
}

// Read retrieves a document by key.
func (e *Engine) Read(key string) ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.closed {
		return nil, ErrEngineShutdown
	}

	return e.lsm.Read(key)
}

// Put stores a document with the given key.
func (e *Engine) Put(key string, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrEngineShutdown
	}

	return e.lsm.Put(key, value)
}

// Delete removes a document by key.
func (e *Engine) Delete(key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrEngineShutdown
	}

	return e.lsm.Delete(key)
}

// BatchPut stores multiple documents atomically-ish.
func (e *Engine) BatchPut(keys []string, values [][]byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrEngineShutdown
	}

	return e.lsm.BatchPut(keys, values)
}

// Keys returns all keys in the store.
// This is optimized to not load values into memory.
func (e *Engine) Keys() ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.closed {
		return nil, ErrEngineShutdown
	}

	return e.lsm.Keys()
}

// ReadKeyRange returns all keys in the range [start, end).
func (e *Engine) ReadKeyRange(start, end string) ([]struct {
	Key   string
	Value []byte
}, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.closed {
		return nil, ErrEngineShutdown
	}

	return e.lsm.ReadKeyRange(start, end)
}

// Count returns the number of documents in the store.
func (e *Engine) Count() int {
	keys, _ := e.Keys()
	return len(keys)
}

// Close closes the engine and releases resources.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	e.closed = true
	return e.lsm.Close()
}
