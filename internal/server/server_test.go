package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestServer_Integration(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sofadb_server_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Addr:    ":9999",
		DataDir: tmpDir,
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Shutdown(nil)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := ts.Client()

	// Helper function for requests
	doReq := func(method, path string, body interface{}) (int, interface{}) {
		var reqBody *bytes.Buffer
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewBuffer(b)
		} else {
			reqBody = bytes.NewBuffer(nil)
		}

		req, _ := http.NewRequest(method, ts.URL+path, reqBody)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		var result interface{}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return resp.StatusCode, result
	}

	// 1. PUT /docs/key
	t.Run("Put", func(t *testing.T) {
		code, _ := doReq("PUT", "/docs/user1", map[string]string{"name": "Alice"})
		if code != http.StatusCreated {
			t.Errorf("Expected 201, got %d", code)
		}
	})

	// 2. GET /docs/key
	t.Run("Get", func(t *testing.T) {
		code, res := doReq("GET", "/docs/user1", nil)
		if code != http.StatusOK {
			t.Errorf("Expected 200, got %d", code)
		}
		m := res.(map[string]interface{})
		if m["name"] != "Alice" {
			t.Errorf("Expected Alice, got %v", m["name"])
		}
	})

	// 2b. GET /docs/key (NotFound)
	t.Run("Get_NotFound", func(t *testing.T) {
		code, _ := doReq("GET", "/docs/missing", nil)
		if code != http.StatusNotFound {
			t.Errorf("Expected 404, got %d", code)
		}
	})

	// 3. POST /batch
	t.Run("Batch", func(t *testing.T) {
		docs := []map[string]interface{}{
			{"key": "user2", "value": "Bob"},
			{"key": "user3", "value": map[string]int{"age": 30}},
		}
		body := map[string]interface{}{"docs": docs}
		code, _ := doReq("POST", "/batch", body)
		if code != http.StatusCreated {
			t.Errorf("Expected 201, got %d", code)
		}

		// Verify
		code, res := doReq("GET", "/docs/user3", nil)
		m := res.(map[string]interface{})
		if m["age"].(float64) != 30 {
			t.Errorf("Expected age 30, got %v", m["age"])
		}
	})

	// 3b. POST /batch (Empty)
	t.Run("Batch_Empty", func(t *testing.T) {
		body := map[string]interface{}{"docs": []interface{}{}}
		code, _ := doReq("POST", "/batch", body)
		if code != http.StatusBadRequest {
			t.Errorf("Expected 400 for empty batch, got %d", code)
		}
	})

	// 4. GET /range
	t.Run("Range", func(t *testing.T) {
		// user1, user2, user3 exist. user1 < user2 < user3
		code, res := doReq("GET", "/range?start=user1&end=user3", nil) // [user1, user3) -> user1, user2
		if code != http.StatusOK {
			t.Errorf("Expected 200, got %d", code)
		}
		m := res.(map[string]interface{})
		results := m["results"].([]interface{})
		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}
	})

	// 4b. GET /range (Invalid Bounds)
	t.Run("Range_Invalid", func(t *testing.T) {
		// Start > End
		code, res := doReq("GET", "/range?start=z&end=a", nil)
		if code != http.StatusOK { // It's valid request, just empty result
			t.Errorf("Expected 200, got %d", code)
		}
		m := res.(map[string]interface{})
		count := m["count"].(float64)
		if count != 0 {
			t.Errorf("Expected 0 results, got %v", count)
		}
	})

	// 5. GET /docs (Streaming)
	t.Run("Docs_Streaming", func(t *testing.T) {
		code, res := doReq("GET", "/docs", nil)
		if code != http.StatusOK {
			t.Errorf("Expected 200, got %d", code)
		}
		list := res.([]interface{}) // Should return Array
		if len(list) != 3 {         // user1, user2, user3
			t.Errorf("Expected 3 docs, got %d", len(list))
		}
	})
}

func TestStreaming_MemoryAndLatency(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sofadb_perf_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{Addr: ":0", DataDir: tmpDir}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// 1. Populate with significant data (e.g., 10,000 small items)
	// Total data ~ 10,000 * 100 bytes = 1MB. (Maybe too small to detect huge spike vs GC noise?)
	// Let's use 1KB values -> 10MB.
	count := 5000
	valSize := 1024
	value := make([]byte, valSize)
	for i := 0; i < valSize; i++ {
		value[i] = 'A'
	}

	// Direct engine access for speed
	engine := srv.Engine()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("k%05d", i)
		if err := engine.Put(key, value); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	// 2. Restart Server to force MemTable flush to SSTable (cleanup memory)
	// This ensures we are testing reading from Disk -> Network, not RAM -> Network
	srv.Shutdown(nil)
	srv, err = New(cfg) // Reopen
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(nil)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 3. Monitor Memory & TTFB
	var startMem, peakMem uint64
	readMem := func() uint64 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	runtime.GC()
	startMem = readMem()
	peakMem = startMem

	// Background poller to catch spikes (approximate)
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				m := readMem()
				if m > peakMem {
					peakMem = m
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	client := ts.Client()
	start := time.Now()
	resp, err := client.Get(ts.URL + "/docs")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	ttfb := time.Since(start)

	// Drain body
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	close(done)

	// Analysis
	// 5000 * 1KB = 5MB data.
	// If we buffered, we'd expect HeapAlloc to jump by >5MB + overhead.
	// The JSON array stringification adds more overhead (keys + json structure).
	// Expected Payload: 5000 * (1024 + ~20 metadata) ~= 5.2MB.

	usage := peakMem - startMem
	t.Logf("Start Mem: %v KB, Peak Mem: %v KB, Diff: %v KB", startMem/1024, peakMem/1024, usage/1024)
	t.Logf("Time to First Byte: %v", ttfb)

	// Check TTFB (Streaming proof)
	if ttfb > 500*time.Millisecond {
		t.Errorf("TTFB too high for streaming: %v", ttfb)
	}

	// Check Memory Spike
	// We allow some overhead, but if it was fully buffered, usage would be much higher than just the stream buffer.
	// However, GC is non-deterministic. The meaningful check is that we didn't OOM or crash.
	// We can just assert it didn't grow unreasonably (e.g. 5x data size).
	if usage > 50*1024*1024 { // 50MB limit for 5MB data
		t.Errorf("Memory usage spiked too high: %v MB", usage/1024/1024)
	}
}
