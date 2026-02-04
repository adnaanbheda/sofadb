package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
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
	target      = flag.String("target", "all", "Target DB: all, sofadb, mongo, redis")
)

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	fmt.Printf("Starting benchmark with N=%d, Concurrency=%d\n\n", *numRequests, *concurrency)

	if *target == "all" || *target == "sofadb" {
		benchSofaDB()
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
func benchSofaDB() {
	fmt.Println("--- SofaDB ---")
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://localhost:8080/docs"

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
