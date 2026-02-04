package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLSMBasic(t *testing.T) {
	dir := "test_data_lsm"
	defer os.RemoveAll(dir)

	lsm, err := NewLSM(dir)
	if err != nil {
		t.Fatalf("NewLSM failed: %v", err)
	}
	defer lsm.Close()

	// 1. Put
	if err := lsm.Put("key1", []byte("value1")); err != nil {
		t.Errorf("Put failed: %v", err)
	}

	// 2. Get
	val, err := lsm.Read("key1")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("Expected value1, got %s", string(val))
	}

	// 3. Update
	if err := lsm.Put("key1", []byte("value1_updated")); err != nil {
		t.Errorf("Put update failed: %v", err)
	}
	val, err = lsm.Read("key1")
	if string(val) != "value1_updated" {
		t.Errorf("Expected value1_updated, got %s", string(val))
	}

	// 4. Delete
	if err := lsm.Delete("key1"); err != nil {
		t.Errorf("Delete failed: %v", err)
	}
	_, err = lsm.Read("key1")
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}
}

func TestLSMFlushAndRecover(t *testing.T) {
	dir := "test_data_flush"
	defer os.RemoveAll(dir)

	// 1. Create engine and write data
	lsm1, err := NewLSM(dir)
	if err != nil {
		t.Fatalf("NewLSM failed: %v", err)
	}

	if err := lsm1.Put("key_persist", []byte("val_persist")); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Force close (no flush, relying on WAL)
	if err := lsm1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 2. Reopen engine and check if data persists
	lsm2, err := NewLSM(dir)
	if err != nil {
		t.Fatalf("Reopen NewLSM failed: %v", err)
	}
	defer lsm2.Close()

	val, err := lsm2.Read("key_persist")
	if err != nil {
		t.Fatalf("Read after recover failed: %v", err)
	}
	if string(val) != "val_persist" {
		t.Errorf("Expected val_persist, got %s", string(val))
	}
}

func TestLSMCompaction(t *testing.T) {
	dir := "test_data_compaction"
	defer os.RemoveAll(dir)

	lsm, err := NewLSM(dir)
	if err != nil {
		t.Fatalf("NewLSM failed: %v", err)
	}
	defer lsm.Close()

	// 1. Create multiple SSTables
	for i := 0; i < 6; i++ {
		key := fmt.Sprintf("k%d", i)
		val := fmt.Sprintf("v%d", i)
		if err := lsm.Put(key, []byte(val)); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
		if err := lsm.ForceFlush(); err != nil {
			t.Fatalf("ForceFlush failed: %v", err)
		}
	}

	// Verify we have 6 tables
	files, _ := filepath.Glob(filepath.Join(dir, "*.sst"))
	if len(files) != 6 {
		t.Errorf("Expected 6 sst files, got %d", len(files))
	}

	// 2. Perform Compaction
	if err := lsm.Compact(); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	// 3. Verify files reduced
	filesAfter, _ := filepath.Glob(filepath.Join(dir, "*.sst"))
	if len(filesAfter) != 1 {
		t.Errorf("Expected 1 sst file after compaction, got %d", len(filesAfter))
	}

	// 4. Verify Data Integrity
	for i := 0; i < 6; i++ {
		key := fmt.Sprintf("k%d", i)
		val, err := lsm.Read(key)
		if err != nil {
			t.Errorf("Read %s failed: %v", key, err)
		}
		if string(val) != fmt.Sprintf("v%d", i) {
			t.Errorf("Expected v%d, got %s", i, string(val))
		}
	}
}

func TestLSMRangeScan(t *testing.T) {
	dir := "test_data_scan"
	defer os.RemoveAll(dir)

	lsm, err := NewLSM(dir)
	if err != nil {
		t.Fatalf("NewLSM failed: %v", err)
	}
	defer lsm.Close()

	data := map[string]string{
		"a": "val_a",
		"b": "val_b",
		"c": "val_c",
		"d": "val_d",
		"e": "val_e",
	}

	for k, v := range data {
		lsm.Put(k, []byte(v))
	}

	// Scan b to d (inclusive start, exclusive end) -> b, c
	res, err := lsm.ReadKeyRange("b", "d")
	if err != nil {
		t.Errorf("ReadKeyRange failed: %v", err)
	}

	if len(res) != 2 {
		t.Errorf("Expected 2 results, got %d", len(res))
		return
	}
	if res[0].Key != "b" || string(res[0].Value) != "val_b" {
		t.Errorf("Unexpected first result: %v", res[0])
	}
	if res[1].Key != "c" || string(res[1].Value) != "val_c" {
		t.Errorf("Unexpected second result: %v", res[1])
	}
}
