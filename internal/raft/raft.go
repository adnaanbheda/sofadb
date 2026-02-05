package raft

import (
	"log"
	"sync"
	"time"
)

// State represents the current state of a Raft server.
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// LogEntry represents a single log entry.
type LogEntry struct {
	Term    int
	Index   int
	Command []byte
}

// ApplyMsg is sent to the ApplyCh when a command is committed.
type ApplyMsg struct {
	CommandValid bool
	Command      []byte
	CommandIndex int
}

// RequestVoteArgs ...
type RequestVoteArgs struct {
	Term         int
	CandidateId  string
	LastLogIndex int
	LastLogTerm  int
}

// RequestVoteReply ...
type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

// AppendEntriesArgs ...
type AppendEntriesArgs struct {
	Term         int
	LeaderId     string
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

// AppendEntriesReply ...
type AppendEntriesReply struct {
	Term          int
	Success       bool
	ConflictTerm  int
	ConflictIndex int
}

// Raft implements the Raft consensus algorithm.
type Raft struct {
	mu        sync.Mutex
	peers     []string // URLs of peers
	me        string   // My URL/ID
	state     State
	applyCh   chan ApplyMsg
	persister *Persister

	// Persistent state on all servers
	currentTerm int
	votedFor    string // ID of candidate we voted for
	log         []LogEntry

	// Volatile state on all servers
	commitIndex int
	lastApplied int

	// Volatile state on leaders
	nextIndex  map[string]int
	matchIndex map[string]int

	// Timers
	electionTimer  *time.Timer
	heartbeatTimer *time.Timer

	// Shutdown
	done chan struct{}
}

// New creates a new Raft instance.
func New(id string, peers []string, applyCh chan ApplyMsg, persister *Persister) *Raft {
	rf := &Raft{
		peers:       peers,
		me:          id,
		state:       Follower,
		applyCh:     applyCh,
		persister:   persister,
		currentTerm: 0,
		votedFor:    "",
		log:         make([]LogEntry, 0),
		commitIndex: 0,
		lastApplied: 0,
		nextIndex:   make(map[string]int),
		matchIndex:  make(map[string]int),
		done:        make(chan struct{}),
	}

	// Recover from persistent state
	rf.readPersist(persister.ReadRaftState())

	if len(rf.log) == 0 {
		// Add dummy entry 0 so real logs start at index 1
		rf.log = append(rf.log, LogEntry{Term: 0, Index: 0, Command: nil})
	}

	// Start the main loop
	go rf.run()

	return rf
}

func (rf *Raft) persist() {
	rf.persister.SaveRaftState(rf.currentTerm, rf.votedFor, rf.log)
}

func (rf *Raft) readPersist(term int, votedFor string, logs []LogEntry, exists bool) {
	if !exists {
		return
	}
	rf.currentTerm = term
	rf.votedFor = votedFor
	rf.log = logs
}

func (rf *Raft) run() {
	rf.resetElectionTimer()
	for {
		select {
		case <-rf.done:
			return
		case <-rf.electionTimer.C:
			rf.startElection()
		}
	}
}

func (rf *Raft) resetElectionTimer() {
	if rf.electionTimer != nil {
		rf.electionTimer.Stop()
	}
	// Randomized timeout 300-600ms
	duration := time.Duration(300+(time.Now().UnixNano()%300)) * time.Millisecond
	rf.electionTimer = time.NewTimer(duration)
}

func (rf *Raft) startElection() {
	rf.mu.Lock()
	if rf.state == Leader {
		rf.mu.Unlock()
		return
	}
	rf.state = Candidate
	rf.currentTerm++
	rf.votedFor = rf.me
	rf.persist() // Persist vote
	log.Printf("[Raft %s] Starting election for Term %d", rf.me, rf.currentTerm)

	rf.resetElectionTimer()

	term := rf.currentTerm
	lastLogIndex := len(rf.log) - 1
	lastLogTerm := rf.log[lastLogIndex].Term
	votesReceived := 1

	rf.mu.Unlock()

	for _, peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		go func(server string) {
			args := RequestVoteArgs{
				Term:         term,
				CandidateId:  rf.me,
				LastLogIndex: lastLogIndex,
				LastLogTerm:  lastLogTerm,
			}
			var reply RequestVoteReply
			if !rf.sendRequestVote(server, &args, &reply) {
				return
			}

			rf.mu.Lock()
			defer rf.mu.Unlock()

			if rf.state != Candidate || rf.currentTerm != term {
				return
			}

			if reply.Term > rf.currentTerm {
				rf.currentTerm = reply.Term
				rf.state = Follower
				rf.votedFor = ""
				rf.persist()
				return
			}

			if reply.VoteGranted {
				votesReceived++
				if votesReceived > len(rf.peers)/2 {
					rf.becomeLeader()
				}
			}
		}(peer)
	}
}

func (rf *Raft) becomeLeader() {
	if rf.state != Candidate {
		return
	}
	rf.state = Leader
	log.Printf("[Raft %s] Elected Leader for Term %d", rf.me, rf.currentTerm)

	for _, peer := range rf.peers {
		rf.nextIndex[peer] = len(rf.log)
		rf.matchIndex[peer] = 0
	}

	go rf.sendHeartbeats()
}

func (rf *Raft) sendHeartbeats() {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}

	for _, peer := range rf.peers {
		if peer == rf.me {
			continue
		}

		prevLogIndex := rf.nextIndex[peer] - 1
		var prevLogTerm int
		if prevLogIndex >= 0 && prevLogIndex < len(rf.log) {
			prevLogTerm = rf.log[prevLogIndex].Term
		}

		entries := make([]LogEntry, 0)
		if rf.nextIndex[peer] < len(rf.log) {
			entries = rf.log[rf.nextIndex[peer]:]
		}

		args := AppendEntriesArgs{
			Term:         rf.currentTerm,
			LeaderId:     rf.me,
			PrevLogIndex: prevLogIndex,
			PrevLogTerm:  prevLogTerm,
			Entries:      entries,
			LeaderCommit: rf.commitIndex,
		}

		go func(server string, args AppendEntriesArgs) {
			var reply AppendEntriesReply
			if !rf.sendAppendEntries(server, &args, &reply) {
				return
			}

			rf.mu.Lock()
			defer rf.mu.Unlock()

			if rf.state != Leader || rf.currentTerm != args.Term {
				return
			}
			if reply.Term > rf.currentTerm {
				rf.currentTerm = reply.Term
				rf.state = Follower
				rf.votedFor = ""
				rf.persist()
				return
			}

			if reply.Success {
				newNext := args.PrevLogIndex + len(args.Entries) + 1
				if newNext > rf.nextIndex[server] {
					rf.nextIndex[server] = newNext
					rf.matchIndex[server] = newNext - 1
				}

				// Update Commit Index
				for N := len(rf.log) - 1; N > rf.commitIndex; N-- {
					count := 1
					for _, p := range rf.peers {
						if p == rf.me {
							continue
						}
						if rf.matchIndex[p] >= N {
							count++
						}
					}
					if count > len(rf.peers)/2 && rf.log[N].Term == rf.currentTerm {
						rf.commitIndex = N
						rf.apply()
						break
					}
				}

			} else {
				// Backtrack optimization
				if reply.ConflictIndex > 0 {
					rf.nextIndex[server] = reply.ConflictIndex
				} else {
					rf.nextIndex[server]--
				}
				if rf.nextIndex[server] < 1 {
					rf.nextIndex[server] = 1
				}
			}
		}(peer, args)
	}
	rf.mu.Unlock()

	time.AfterFunc(100*time.Millisecond, rf.sendHeartbeats)
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.state = Follower
		rf.votedFor = ""
		rf.persist()
	}

	reply.Term = rf.currentTerm
	reply.VoteGranted = false

	if args.Term < rf.currentTerm {
		return nil
	}

	upToDate := false
	lastIndex := len(rf.log) - 1
	lastTerm := rf.log[lastIndex].Term

	if args.LastLogTerm > lastTerm {
		upToDate = true
	} else if args.LastLogTerm == lastTerm && args.LastLogIndex >= lastIndex {
		upToDate = true
	}

	if (rf.votedFor == "" || rf.votedFor == args.CandidateId) && upToDate {
		rf.votedFor = args.CandidateId
		rf.state = Follower
		rf.resetElectionTimer()
		rf.persist()
		reply.VoteGranted = true
	}

	return nil
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.state = Follower
		rf.votedFor = ""
		rf.persist()
	}

	reply.Term = rf.currentTerm
	reply.Success = false

	if args.Term < rf.currentTerm {
		return nil
	}

	rf.resetElectionTimer()

	if len(rf.log) <= args.PrevLogIndex {
		reply.ConflictIndex = len(rf.log)
		reply.ConflictTerm = -1
		return nil
	}

	if rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		reply.ConflictTerm = rf.log[args.PrevLogIndex].Term
		for i := args.PrevLogIndex; i >= 0; i-- {
			if rf.log[i].Term == reply.ConflictTerm {
				reply.ConflictIndex = i
			} else {
				break
			}
		}
		return nil
	}

	for i, entry := range args.Entries {
		idx := args.PrevLogIndex + 1 + i
		if idx < len(rf.log) {
			if rf.log[idx].Term != entry.Term {
				rf.log = rf.log[:idx]
				rf.log = append(rf.log, entry)
			}
		} else {
			rf.log = append(rf.log, entry)
		}
	}

	if len(args.Entries) > 0 {
		rf.persist()
	}

	if args.LeaderCommit > rf.commitIndex {
		lastNewIndex := args.PrevLogIndex + len(args.Entries)
		if args.LeaderCommit < lastNewIndex {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = lastNewIndex
		}
		rf.apply()
	}

	reply.Success = true
	return nil
}

func (rf *Raft) apply() {
	for rf.lastApplied < rf.commitIndex {
		rf.lastApplied++
		msg := ApplyMsg{
			CommandValid: true,
			Command:      rf.log[rf.lastApplied].Command,
			CommandIndex: rf.lastApplied,
		}
		rf.applyCh <- msg
	}
}

func (rf *Raft) Propose(command []byte) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return -1, -1, false
	}

	index := len(rf.log)
	term := rf.currentTerm
	rf.log = append(rf.log, LogEntry{Term: term, Index: index, Command: command})
	rf.persist()

	return index, term, true
}

func (rf *Raft) Close() {
	close(rf.done)
}
