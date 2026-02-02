// SofaDB - A lightweight key-document database with HTTP API
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sofadb/internal/server"
)

var (
	version = "0.1.0"
)

func main() {
	// Parse command line flags
	port := flag.Int("port", 8080, "Port to listen on")
	dataDir := flag.String("data-dir", "./data", "Directory for data storage")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("SofaDB v%s\n", version)
		os.Exit(0)
	}

	// Create server configuration
	cfg := server.Config{
		Addr:    fmt.Sprintf(":%d", *port),
		DataDir: *dataDir,
	}

	// Create and start server
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Handle graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	log.Printf("SofaDB v%s started on port %d", version, *port)
	log.Printf("Data directory: %s", *dataDir)

	// Wait for shutdown signal
	<-shutdown

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}

	log.Println("SofaDB stopped")
}
