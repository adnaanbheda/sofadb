// Package server provides the HTTP server for SofaDB.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"sofadb/internal/engine"
)

// Server represents the HTTP server for SofaDB.
type Server struct {
	engine     *engine.Engine
	httpServer *http.Server
	addr       string
}

// Config holds the server configuration.
type Config struct {
	Addr    string // Address to listen on (e.g., ":8080")
	DataDir string // Directory for data storage
}

// Engine returns the underlying storage engine.
func (s *Server) Engine() *engine.Engine {
	return s.engine
}

// New creates a new Server with the given configuration.
func New(cfg Config) (*Server, error) {
	eng, err := engine.New(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create engine: %w", err)
	}

	s := &Server{
		engine: eng,
		addr:   cfg.Addr,
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.loggingMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

// Handler returns the HTTP handler for testing purposes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return s.loggingMiddleware(mux)
}

// registerRoutes sets up the HTTP routes.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/docs", s.handleDocs)
	mux.HandleFunc("/docs/", s.handleDoc)
	mux.HandleFunc("/range", s.handleRange)
	mux.HandleFunc("/batch", s.handleBatch)
}

// loggingMiddleware logs incoming requests.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	log.Printf("SofaDB server starting on %s", s.addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down server...")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}
	return s.engine.Close()
}

// handleHealth responds to health check requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"documents": s.engine.Count(),
	})
}

// handleDocs streams all documents as a JSON array.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Start JSON array
	w.Write([]byte("["))

	first := true
	enc := json.NewEncoder(w)

	err := s.engine.Scan(func(key string, value []byte) bool {
		if !first {
			w.Write([]byte(","))
		}
		first = false

		// Check if value is valid JSON
		var jsonVal interface{}
		if json.Valid(value) {
			jsonVal = json.RawMessage(value)
		} else {
			jsonVal = string(value)
		}

		doc := struct {
			Key   string      `json:"key"`
			Value interface{} `json:"value"`
		}{
			Key:   key,
			Value: jsonVal,
		}

		if encodeErr := enc.Encode(doc); encodeErr != nil {
			log.Printf("Error encoding doc: %v", encodeErr)
			return false // Stop iteration
		}
		return true // Continue iteration
	})

	if err != nil {
		// Note: If we already wrote part of the response, we can't change the status code now.
		// We just log it. In a robust system, we might use trailers or a chunked stream error convention.
		log.Printf("Scan error: %v", err)
	}

	// End JSON array
	w.Write([]byte("]"))
}

// handleDoc handles requests to /docs/{key}.
func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	// Extract key from path
	key := strings.TrimPrefix(r.URL.Path, "/docs/")
	if key == "" {
		http.Error(w, "Key is required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGet(w, r, key)
	case http.MethodPut:
		s.handlePut(w, r, key)
	case http.MethodDelete:
		s.handleDelete(w, r, key)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGet retrieves a document by key.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, key string) {
	value, err := s.engine.Read(key)
	if err != nil {
		if err == engine.ErrKeyNotFound {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(value)
}

// handlePut stores or updates a document.
func (s *Server) handlePut(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Validate JSON
	if !json.Valid(body) {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := s.engine.Put(key, body); err != nil {
		if err == engine.ErrKeyTooLong {
			http.Error(w, "Key too long (max 256 bytes)", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "created",
		"key":    key,
	})
}

// handleDelete removes a document by key.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, key string) {
	if err := s.engine.Delete(key); err != nil {
		if err == engine.ErrKeyNotFound {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "deleted",
		"key":    key,
	})
}

// handleRange handles requests to /range?start=foo&end=bar.
func (s *Server) handleRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	if start == "" {
		http.Error(w, "Start key is required", http.StatusBadRequest)
		return
	}
	if end == "" {
		http.Error(w, "End key is required", http.StatusBadRequest)
		return
	}

	results, err := s.engine.ReadKeyRange(start, end)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Transform for JSON
	type rangePair struct {
		Key   string      `json:"key"`
		Value interface{} `json:"value"`
	}
	var jsonResults []rangePair
	for _, res := range results {
		var val interface{}
		if json.Valid(res.Value) {
			val = json.RawMessage(res.Value)
		} else {
			val = string(res.Value)
		}

		jsonResults = append(jsonResults, rangePair{
			Key:   res.Key,
			Value: val,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results": jsonResults,
		"count":   len(jsonResults),
	})
}

// handleBatch handles requests to POST /batch.
// Body: {"docs": [{"key": "k1", "value": "v1"}, ...]}
func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Docs []struct {
			Key   string      `json:"key"`
			Value interface{} `json:"value"`
		} `json:"docs"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(req.Docs) == 0 {
		http.Error(w, "No documents provided", http.StatusBadRequest)
		return
	}

	// Prepare data for Engine.BatchPut
	keys := make([]string, len(req.Docs))
	values := make([][]byte, len(req.Docs))

	for i, doc := range req.Docs {
		if doc.Key == "" {
			http.Error(w, fmt.Sprintf("Key required at index %d", i), http.StatusBadRequest)
			return
		}
		keys[i] = doc.Key

		// Handle value conversion similar to Put
		var valBytes []byte
		switch v := doc.Value.(type) {
		case string:
			valBytes = []byte(v)
		case nil:
			valBytes = []byte("")
		default:
			// If it's a JSON object/array, marshal it back to bytes
			b, err := json.Marshal(v)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid value at index %d: %v", i, err), http.StatusBadRequest)
				return
			}
			valBytes = b
		}
		values[i] = valBytes
	}

	if err := s.engine.BatchPut(keys, values); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "created",
		"count":  len(keys),
	})
}
