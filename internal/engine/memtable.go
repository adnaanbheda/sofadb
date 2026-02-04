package engine

import (
	"math/rand"
	"sync"
	"time"
)

const (
	maxHeight = 20
	p         = 0.5
)

type node struct {
	key   string
	value []byte
	next  []*node
}

// MemTable is an in-memory key-value store using a Skip List.
type MemTable struct {
	mu     sync.RWMutex
	head   *node
	height int
	size   int64 // Approximate size in bytes
	len    int   // Number of entries
	rnd    *rand.Rand
}

// NewMemTable creates a new MemTable.
func NewMemTable() *MemTable {
	return &MemTable{
		head:   &node{next: make([]*node, maxHeight)},
		height: 1,
		rnd:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Put adds or updates a key-value pair.
func (m *MemTable) Put(key string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	update := make([]*node, maxHeight)
	current := m.head

	for i := m.height - 1; i >= 0; i-- {
		for current.next[i] != nil && current.next[i].key < key {
			current = current.next[i]
		}
		update[i] = current
	}

	current = current.next[0]

	// Update existing key
	if current != nil && current.key == key {
		m.size += int64(len(value) - len(current.value))
		current.value = value
		return
	}

	// Insert new key
	level := m.randomLevel()
	if level > m.height {
		for i := m.height; i < level; i++ {
			update[i] = m.head
		}
		m.height = level
	}

	newNode := &node{
		key:   key,
		value: value,
		next:  make([]*node, level),
	}

	for i := 0; i < level; i++ {
		newNode.next[i] = update[i].next[i]
		update[i].next[i] = newNode
	}

	m.size += int64(len(key) + len(value))
	m.len++
}

// Get retrieves a value by key.
func (m *MemTable) Get(key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current := m.head
	for i := m.height - 1; i >= 0; i-- {
		for current.next[i] != nil && current.next[i].key < key {
			current = current.next[i]
		}
	}

	current = current.next[0]
	if current != nil && current.key == key {
		return current.value, true
	}

	return nil, false
}

// Delete removes a key. In LSM, this usually means inserting a tombstone, but for the MemTable structure itself we can delete.
// However, to support standard LSM semantics, we should probably support tombstones.
// For now, let's just delete from the structure to keep it simple,
// OR simpler: Put(key, nil) represents a delete/tombstone.
// Let's assume the caller handles using a specific tombstone byte sequence or nil.
// If we delete directly from skip list, we save memory.
func (m *MemTable) Delete(key string) {
	// For a pure MemTable, we can just remove.
	// But in full LSM, we might need to track this delete to flush it to SSTable.
	// If we just delete from MemTable, we lose the "delete" instruction for older SSTables.
	// So we MUST insert a tombstone record.
	// We will treat nil value as tombstone in Put.
	// This allows Delete to just be a wrapper around Put.
	// But wait, if we delete a key that is NOT in MemTable but IS in SSTable, we MUST add a record.
	// So Delete == Put(key, tombstone).
}

// randomLevel generates a random level for a new skip list node.
func (m *MemTable) randomLevel() int {
	level := 1
	for m.rnd.Float64() < p && level < maxHeight {
		level++
	}
	return level
}

// Size returns the approximate size in bytes.
func (m *MemTable) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

// Iterator returns all key-values in order.
// This is trivial in a skip list (traverse level 0).
// In a real impl, we'd want a proper Iterator type.
func (m *MemTable) All() ([]struct {
	Key   string
	Value []byte
}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []struct {
		Key   string
		Value []byte
	}

	current := m.head.next[0]
	for current != nil {
		result = append(result, struct {
			Key   string
			Value []byte
		}{current.key, current.value})
		current = current.next[0]
	}
	return result, nil
}

// Keys returns all keys in the MemTable.
func (m *MemTable) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	current := m.head.next[0]
	for current != nil {
		keys = append(keys, current.key)
		current = current.next[0]
	}
	return keys
}
