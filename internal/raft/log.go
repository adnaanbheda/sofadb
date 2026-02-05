package raft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Persister handles saving and loading Raft state to disk.
type Persister struct {
	mu       sync.Mutex
	filePath string
}

type state struct {
	CurrentTerm int
	VotedFor    string
	Log         []LogEntry
}

func NewPersister(dir string) *Persister {
	// Ensure dir exists
	os.MkdirAll(dir, 0755)
	return &Persister{
		filePath: filepath.Join(dir, "raft_state.json"),
	}
}

func (p *Persister) SaveRaftState(currentTerm int, votedFor string, logs []LogEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()

	s := state{
		CurrentTerm: currentTerm,
		VotedFor:    votedFor,
		Log:         logs,
	}

	data, err := json.Marshal(s)
	if err != nil {
		return // Should panic or log?
	}

	// Atomic write
	tmpImp := p.filePath + ".tmp"
	if err := os.WriteFile(tmpImp, data, 0644); err != nil {
		return
	}
	os.Rename(tmpImp, p.filePath)
}

func (p *Persister) ReadRaftState() (int, string, []LogEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(p.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", nil, false
		}
		return 0, "", nil, false
	}

	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, "", nil, false
	}

	return s.CurrentTerm, s.VotedFor, s.Log, true
}
