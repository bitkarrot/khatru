#!/bin/bash

# Create data directories if they don't exist
mkdir -p ./data/media_cache

# Build the pfpcache relay
echo "Building pfpcache relay..."
go build -o pfpcache-relay main.go

# Check if build was successful
if [ $? -ne 0 ]; then
    echo "Build failed. Please check the errors above."
    exit 1
fi

# Run the relay
echo "Starting pfpcache relay on http://localhost:8080"
echo "Press Ctrl+C to stop"
./pfpcache-relay
