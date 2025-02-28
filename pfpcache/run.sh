#!/bin/bash

# Create data directories if they don't exist
mkdir -p ./data/media_cache

# Build and run the pfpcache relay
go build -o pfpcache-relay main.go
./pfpcache-relay
