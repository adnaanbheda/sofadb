package raft

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// sendRequestVote sends a RequestVote RPC to a server.
func (rf *Raft) sendRequestVote(server string, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	url := server + "/raft/request_vote"

	data, err := json.Marshal(args)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 100 * time.Millisecond}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		return false
	}

	return true
}

// sendAppendEntries sends an AppendEntries RPC to a server.
func (rf *Raft) sendAppendEntries(server string, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	url := server + "/raft/append_entries"

	data, err := json.Marshal(args)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 100 * time.Millisecond}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		return false
	}

	return true
}
