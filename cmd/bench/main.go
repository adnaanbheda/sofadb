package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	numRequests = flag.Int("n", 10000, "Number of requests")
	concurrency = flag.Int("c", 10, "Concurrency level")
	target      = flag.String("target", "all", "Target DB: all, sofadb-http, sofadb-tcp, mongo, redis")
)

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	fmt.Printf("Starting benchmark with N=%d, Concurrency=%d\n\n", *numRequests, *concurrency)

	if *target == "all" || *target == "sofadb-http" {
		benchSofaDBHTTP()
	}
	if *target == "all" || *target == "sofadb-tcp" {
		benchSofaDBTCP()
	}
	if *target == "all" || *target == "mongo" {
		benchMongo()
	}
	if *target == "all" || *target == "redis" {
		benchRedis()
	}
}

func runBenchmark(name string, op func(int) error) {
	start := time.Now()
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, *concurrency)

	errCount := 0
	var mu sync.Mutex

	for i := 0; i < *numRequests; i++ {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(id int) {
			defer wg.Done()
			defer func() { <-semaphore }()
			if err := op(id); err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	duration := time.Since(start)
	qps := float64(*numRequests) / duration.Seconds()

	fmt.Printf("[%s] Time: %v | QPS: %.2f | Errors: %d\n", name, duration, qps, errCount)
}

// --- SofaDB Benchmark ---
func benchSofaDBHTTP() {
	fmt.Println("--- SofaDB ---")
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://localhost:9696/docs"

	// Write
	runBenchmark("SofaDB Write", func(i int) error {
		key := fmt.Sprintf("k%d", i)
		val := fmt.Sprintf(`{"name": "user%d", "data": "some-random-data-%d"}`, i, rand.Int())
		req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/%s", url, key), bytes.NewBuffer([]byte(val)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	})

	// Read
	runBenchmark("SofaDB Read ", func(i int) error {
		key := fmt.Sprintf("k%d", rand.Intn(*numRequests)) // Random read
		resp, err := client.Get(fmt.Sprintf("%s/%s", url, key))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode != 404 { // 404 is valid for random
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	})
}

func benchSofaDBTCP() {
	fmt.Println("--- SofaDB TCP ---")
	addr := "localhost:9091"

	// Simple connection pooling via channel
	pool := make(chan net.Conn, *concurrency)
	for i := 0; i < *concurrency; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			log.Fatalf("TCP dial error: %v", err)
		}
		pool <- conn
	}

	// Write
	runBenchmark("SofaDB TCP Write", func(i int) error {
		conn := <-pool
		defer func() { pool <- conn }()

		key := fmt.Sprintf("k%d", i)
		val := fmt.Sprintf(`{"name": "user%d", "data": "some-random-data-%d"}`, i, rand.Int())

		buf := new(bytes.Buffer)
		buf.WriteByte(0x01) // Put
		binary.Write(buf, binary.LittleEndian, int16(len(key)))
		buf.WriteString(key)
		binary.Write(buf, binary.LittleEndian, int32(len(val)))
		buf.WriteString(val)

		conn.Write(buf.Bytes())

		statusBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, statusBuf); err != nil {
			return err
		}

		var vLen int32
		if err := binary.Read(conn, binary.LittleEndian, &vLen); err != nil {
			return err
		}
		if vLen > 0 {
			io.CopyN(io.Discard, conn, int64(vLen))
		}

		if statusBuf[0] != 0 {
			return fmt.Errorf("status %d", statusBuf[0])
		}
		return nil
	})

	// Read
	runBenchmark("SofaDB TCP Read ", func(i int) error {
		conn := <-pool
		defer func() { pool <- conn }()

		key := fmt.Sprintf("k%d", rand.Intn(*numRequests))

		buf := new(bytes.Buffer)
		buf.WriteByte(0x02) // Read
		binary.Write(buf, binary.LittleEndian, int16(len(key)))
		buf.WriteString(key)

		conn.Write(buf.Bytes())

		statusBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, statusBuf); err != nil {
			return err
		}

		var vLen int32
		if err := binary.Read(conn, binary.LittleEndian, &vLen); err != nil {
			return err
		}
		if vLen > 0 {
			io.CopyN(io.Discard, conn, int64(vLen))
		}

		if statusBuf[0] == 0x02 {
			return nil
		} // NotFound is 0x02
		if statusBuf[0] != 0 {
			return fmt.Errorf("status %d", statusBuf[0])
		}
		return nil
	})
}

// --- MongoDB Benchmark ---
func benchMongo() {
	fmt.Println("--- MongoDB ---")
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Printf("Mongo connect error: %v", err)
		return
	}
	defer client.Disconnect(ctx)
	col := client.Database("bench").Collection("test")

	// Write
	runBenchmark("MongoDB Write", func(i int) error {
		_, err := col.InsertOne(ctx, bson.M{"key": fmt.Sprintf("k%d", i), "value": fmt.Sprintf("v%d", i)})
		return err
	})

	// Read
	runBenchmark("MongoDB Read ", func(i int) error {
		r := rand.Intn(*numRequests)
		res := col.FindOne(ctx, bson.M{"key": fmt.Sprintf("k%d", r)})
		return res.Err()
	})
}

// --- Redis Benchmark ---
func benchRedis() {
	fmt.Println("--- Redis ---")
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	ctx := context.Background()

	// Write
	runBenchmark("Redis Write", func(i int) error {
		return rdb.Set(ctx, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i), 0).Err()
	})

	// Read
	runBenchmark("Redis Read ", func(i int) error {
		r := rand.Intn(*numRequests)
		return rdb.Get(ctx, fmt.Sprintf("k%d", r)).Err()
	})
}
