#!/bin/bash
for i in {1..10000}; do
  curl -X PUT http://localhost:9000/docs/user$i \
       -H "Content-Type: application/json" \
       -d '{"name": "Alice", "email": "alice@example.com"}'
  # Optional: add a short sleep to avoid overwhelming the server
  # sleep 0.001
done