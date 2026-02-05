package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRaft_Cluster(t *testing.T) {
	// Setup 3 nodes
	nodeCount := 3
	servers := make([]*httptest.Server, nodeCount)
	configs := make([]Config, nodeCount)
	dirs := make([]string, nodeCount)

	// 1. Create Servers (without peers first to get URLs)
	// Actually we need URLs to config peers. httptest assigns random ports.
	// So we must start them, get URL, then stop/restart or use valid config?
	// Problem: Raft needs peer list at startup.
	// Solution: Create listener first? Or just use fixed ports for test?
	// Using httptest is tricky because it picks port on Start.
	// Let's use fixed ports for local test: 9001, 9002, 9003.
	// But we can't easily bind specific port with httptest.

	// Alternative: Start servers with empty peers, then update config? No, config is immutable in my design.
	// Workaround: Use a deterministic port allocator or just guess.
	// Better: Start 3 listeners, get ports, then start servers.

	// SIMPLER: Just use the `httptest.NewUnstartedServer`, get its Listener address, then configure.

	peers := make([]string, nodeCount)
	instances := make([]*Server, nodeCount)

	for i := 0; i < nodeCount; i++ {
		ts := httptest.NewUnstartedServer(nil)
		servers[i] = ts
		peers[i] = ts.Listener.Addr().String() // This might be "127.0.0.1:12345"
		// We need http:// prefix for peer list
		peers[i] = "http://" + peers[i]
	}

	// 2. Initialize and Start
	for i := 0; i < nodeCount; i++ {
		dir, _ := os.MkdirTemp("", fmt.Sprintf("raft_node_%d", i))
		dirs[i] = dir
		defer os.RemoveAll(dir)

		cfg := Config{
			Addr:    servers[i].Listener.Addr().String(),
			DataDir: dir,
			RaftID:  peers[i],
			Peers:   peers,
		}
		configs[i] = cfg

		srv, err := New(cfg)
		if err != nil {
			t.Fatalf("Failed to create server %d: %v", i, err)
		}
		instances[i] = srv

		// Update the handler of the unstarted server
		servers[i].Config.Handler = srv.Handler()
		servers[i].Start()
	}
	defer func() {
		for _, ts := range servers {
			ts.Close()
		}
	}()

	// 3. Wait for Leader Election
	t.Log("Waiting for leader election...")
	time.Sleep(5 * time.Second) // Give enough time for election

	leaderIndex := -1
	for i, srv := range instances {
		// We can't easily access internal Raft state to check leader.
		// But we can try a PUT. Follower returns 307.
		// Or check /health? No raft info there.
		// Let's try PUT to each until one accepts.

		client := servers[i].Client()
		body := []byte(`{"value": "test"}`)
		req, _ := http.NewRequest("PUT", servers[i].URL+"/docs/key1", bytes.NewBuffer(body))
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusCreated {
			leaderIndex = i
			t.Logf("Node %d is likely leader (accepted PUT)", i)
			break
		}
	}

	if leaderIndex == -1 {
		t.Fatal("No leader found (all rejected PUT or failed)")
	}

	// 4. Verify Replication
	t.Log("Verifying replication...")
	time.Sleep(1 * time.Second) // Wait for propagation

	checkCount := 0
	for i, ts := range servers {
		if i == leaderIndex {
			continue
		}

		resp, err := ts.Client().Get(ts.URL + "/docs/key1")
		if err != nil {
			t.Errorf("Node %d Get failed: %v", i, err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			checkCount++
		} else {
			t.Logf("Node %d returned %d (might not have replicated yet)", i, resp.StatusCode)
		}
	}

	if checkCount == 0 {
		t.Error("Replication failed: followers do not have the key")
	} else {
		t.Logf("Replication successful to %d followers", checkCount)
	}
}
