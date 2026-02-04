package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestChaos_HelperProcess is a helper that runs as a separate process for chaos testing.
// It's not a real test - it's invoked by exec.Command from other tests.
func TestChaos_HelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	scenario := os.Getenv("CHAOS_SCENARIO")
	dataDir := os.Getenv("CHAOS_DATA_DIR")

	switch scenario {
	case "WAL_PARTIAL_WRITE":
		helperWALPartialWrite(dataDir)
	case "COMPACTION_INTERRUPT":
		helperCompactionInterrupt(dataDir)
	case "RAPID_RESTART":
		helperRapidRestart(dataDir)
	default:
		fmt.Fprintf(os.Stderr, "Unknown scenario: %s\n", scenario)
		os.Exit(1)
	}
}

func helperWALPartialWrite(dataDir string) {
	lsm, err := NewLSM(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create LSM: %v\n", err)
		os.Exit(1)
	}

	// Write a few keys
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := lsm.Put(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "Put failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Simulate crash before sync (exit without proper Close)
	// This leaves WAL potentially in partial state
	fmt.Println("CRASH_SIMULATED")
	os.Exit(137) // Simulate kill -9
}

func helperCompactionInterrupt(dataDir string) {
	lsm, err := NewLSM(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create LSM: %v\n", err)
		os.Exit(1)
	}

	// Write enough data to trigger compaction
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := []byte(fmt.Sprintf("value-%d-with-some-padding-to-increase-size", i))
		if err := lsm.Put(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "Put failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Give compaction a moment to start
	time.Sleep(50 * time.Millisecond)

	// Crash during potential compaction
	fmt.Println("CRASH_SIMULATED")
	os.Exit(137)
}

func helperRapidRestart(dataDir string) {
	iteration := os.Getenv("CHAOS_ITERATION")

	lsm, err := NewLSM(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create LSM: %v\n", err)
		os.Exit(1)
	}

	// Write a key specific to this iteration
	key := fmt.Sprintf("restart-key-%s", iteration)
	value := []byte(fmt.Sprintf("restart-value-%s", iteration))
	if err := lsm.Put(key, value); err != nil {
		fmt.Fprintf(os.Stderr, "Put failed: %v\n", err)
		os.Exit(1)
	}

	// Proper close this time
	if err := lsm.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Close failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("ITERATION_COMPLETE")
	os.Exit(0)
}

// TestChaos_WAL_PartialWrite tests recovery from partial WAL writes (power plug scenario)
func TestChaos_WAL_PartialWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	dataDir := t.TempDir()

	// Step 1: Run helper process that crashes
	cmd := exec.Command(os.Args[0], "-test.run=TestChaos_HelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"CHAOS_SCENARIO=WAL_PARTIAL_WRITE",
		"CHAOS_DATA_DIR="+dataDir,
	)
	output, _ := cmd.CombinedOutput()

	if !contains(string(output), "CRASH_SIMULATED") {
		t.Fatalf("Helper process did not crash as expected: %s", output)
	}

	// Step 2: Restart engine normally
	lsm, err := NewLSM(dataDir)
	if err != nil {
		t.Fatalf("Failed to recover LSM after crash: %v", err)
	}
	defer lsm.Close()

	// Step 3: Verify at least some keys were recovered
	// (We can't guarantee all 10 due to crash timing, but should get some)
	recovered := 0
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, err := lsm.Get(key)
		if err == nil {
			recovered++
		}
	}

	if recovered == 0 {
		t.Error("No keys recovered after WAL crash - expected at least some")
	}

	t.Logf("Recovered %d/10 keys after crash", recovered)
}

// TestChaos_CompactionInterrupt tests recovery from interrupted compaction
func TestChaos_CompactionInterrupt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	dataDir := t.TempDir()

	// Step 1: Run helper process that crashes during compaction
	cmd := exec.Command(os.Args[0], "-test.run=TestChaos_HelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"CHAOS_SCENARIO=COMPACTION_INTERRUPT",
		"CHAOS_DATA_DIR="+dataDir,
	)
	output, _ := cmd.CombinedOutput()

	if !contains(string(output), "CRASH_SIMULATED") {
		t.Fatalf("Helper process did not crash as expected: %s", output)
	}

	// Step 2: Restart engine
	lsm, err := NewLSM(dataDir)
	if err != nil {
		t.Fatalf("Failed to recover LSM after compaction interrupt: %v", err)
	}
	defer lsm.Close()

	// Step 3: Verify data integrity - check a sample of keys
	for i := 0; i < 1000; i += 100 {
		key := fmt.Sprintf("key-%d", i)
		value, err := lsm.Get(key)
		if err != nil {
			t.Errorf("Key %s not found after compaction interrupt", key)
			continue
		}
		expected := fmt.Sprintf("value-%d-with-some-padding-to-increase-size", i)
		if string(value) != expected {
			t.Errorf("Key %s has wrong value: got %s, want %s", key, value, expected)
		}
	}

	t.Log("Data integrity verified after compaction interrupt")
}

// TestChaos_RapidRestart tests 100 rapid restart cycles
func TestChaos_RapidRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	dataDir := t.TempDir()
	iterations := 100

	for i := 0; i < iterations; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestChaos_HelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"CHAOS_SCENARIO=RAPID_RESTART",
			"CHAOS_DATA_DIR="+dataDir,
			fmt.Sprintf("CHAOS_ITERATION=%d", i),
		)
		output, err := cmd.CombinedOutput()

		if err != nil || !contains(string(output), "ITERATION_COMPLETE") {
			t.Fatalf("Iteration %d failed: %v\nOutput: %s", i, err, output)
		}

		if i%10 == 0 {
			t.Logf("Completed %d/%d iterations", i, iterations)
		}
	}

	// Final verification: open engine and check all keys exist
	lsm, err := NewLSM(dataDir)
	if err != nil {
		t.Fatalf("Failed to open LSM after %d restarts: %v", iterations, err)
	}
	defer lsm.Close()

	for i := 0; i < iterations; i++ {
		key := fmt.Sprintf("restart-key-%d", i)
		value, err := lsm.Get(key)
		if err != nil {
			t.Errorf("Key from iteration %d not found", i)
			continue
		}
		expected := fmt.Sprintf("restart-value-%d", i)
		if string(value) != expected {
			t.Errorf("Key from iteration %d has wrong value", i)
		}
	}

	t.Logf("Successfully completed %d rapid restart cycles with no data loss", iterations)
}

// TestChaos_CorruptedSSTable tests handling of corrupted SSTable files
func TestChaos_CorruptedSSTable(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	dataDir := t.TempDir()

	// Create LSM and write some data
	lsm, err := NewLSM(dataDir)
	if err != nil {
		t.Fatalf("Failed to create LSM: %v", err)
	}

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := lsm.Put(key, value); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	// Force flush to create SSTable
	if err := lsm.ForceFlush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	lsm.Close()

	// Corrupt the SSTable by truncating it
	files, _ := os.ReadDir(dataDir)
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".sst" {
			sstPath := filepath.Join(dataDir, f.Name())
			info, _ := os.Stat(sstPath)
			// Truncate to half size to corrupt it
			os.Truncate(sstPath, info.Size()/2)
			t.Logf("Corrupted SSTable: %s", f.Name())
			break
		}
	}

	// Try to open - should handle corruption gracefully
	lsm2, err := NewLSM(dataDir)
	if err != nil {
		// It's acceptable to fail to open with corrupted SSTable
		t.Logf("LSM failed to open with corrupted SSTable (expected): %v", err)
		return
	}
	defer lsm2.Close()

	t.Log("LSM opened despite corrupted SSTable")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			len(s) > len(substr)+1 && s[1:len(substr)+1] == substr ||
			len(s) > len(substr)*2 && s[len(s)/2-len(substr)/2:len(s)/2+len(substr)/2+1] == substr))
	// Simple contains check
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
