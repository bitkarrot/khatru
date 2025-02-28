package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fiatjaf/eventstore/sqlite3"
	"github.com/fiatjaf/khatru"
	"github.com/fiatjaf/khatru/blossom"
	"github.com/nbd-wtf/go-nostr"
)

// ProfileMetadata represents the structure of a Nostr profile metadata (kind 0)
type ProfileMetadata struct {
	Name        string `json:"name,omitempty"`
	About       string `json:"about,omitempty"`
	Picture     string `json:"picture,omitempty"`
	Banner      string `json:"banner,omitempty"`
	NIP05       string `json:"nip05,omitempty"`
	LUD16       string `json:"lud16,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// Config holds the application configuration
type Config struct {
	ListenAddr      string
	DatabasePath    string
	MediaCachePath  string
	UpstreamRelays  []string
	MaxConcurrent   int
	CacheExpiration time.Duration
}

func main() {
	// Initialize configuration
	config := Config{
		ListenAddr:      ":8080",
		DatabasePath:    "./data/pfpcache.db",
		MediaCachePath:  "./data/media_cache",
		UpstreamRelays:  []string{"wss://relay.damus.io", "wss://relay.nostr.band", "wss://nos.lol"},
		MaxConcurrent:   20,
		CacheExpiration: 24 * time.Hour * 7, // Cache images for 7 days
	}

	// Ensure directories exist
	os.MkdirAll(filepath.Dir(config.DatabasePath), 0755)
	os.MkdirAll(config.MediaCachePath, 0755)

	// Initialize Khatru relay
	relay := khatru.NewRelay()
	relay.Info.Name = "Profile Picture Cache Relay"
	relay.Info.Description = "A Nostr relay that caches profile pictures"
	relay.Info.SupportedNIPs = []any{1, 11, 40, 42}

	// Set up SQLite database for event storage
	db := sqlite3.SQLite3Backend{DatabaseURL: config.DatabasePath}
	if err := db.Init(); err != nil {
		log.Fatalf("Failed to initialize SQLite database: %v", err)
	}

	// Connect relay to database
	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	relay.QueryEvents = append(relay.QueryEvents, db.QueryEvents)
	relay.CountEvents = append(relay.CountEvents, db.CountEvents)
	relay.DeleteEvent = append(relay.DeleteEvent, db.DeleteEvent)
	relay.ReplaceEvent = append(relay.ReplaceEvent, db.ReplaceEvent)

	// Initialize Blossom for media storage
	storage := blossom.NewFilesystemStorage(config.MediaCachePath)
	blossomHandler := blossom.NewHandler(storage)

	// Create a mux for our HTTP handlers
	mux := http.NewServeMux()

	// Register the Blossom handler for media serving
	mux.Handle("/media/", http.StripPrefix("/media/", blossomHandler))

	// Set up batch profile caching endpoint
	mux.HandleFunc("/cache-profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse the request body for pubkeys
		var request struct {
			Pubkeys []string `json:"pubkeys"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Start fetching and caching in the background
		go fetchAndCacheProfiles(relay, storage, request.Pubkeys, config)

		// Return immediately to the client
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Profile caching initiated",
			"count":   fmt.Sprintf("%d profiles queued", len(request.Pubkeys)),
		})
	})

	// Set up profile picture endpoint
	mux.HandleFunc("/profile-pic/", func(w http.ResponseWriter, r *http.Request) {
		// Extract pubkey from path
		pubkey := strings.TrimPrefix(r.URL.Path, "/profile-pic/")
		if pubkey == "" {
			http.Error(w, "Missing pubkey", http.StatusBadRequest)
			return
		}

		// Find the latest profile for this pubkey
		filter := nostr.Filter{
			Kinds:   []int{0},
			Authors: []string{pubkey},
			Limit:   1,
		}

		events, err := queryEvents(relay, filter)
		if err != nil || len(events) == 0 {
			http.Error(w, "Profile not found", http.StatusNotFound)
			return
		}

		// Parse profile metadata
		var metadata ProfileMetadata
		if err := json.Unmarshal([]byte(events[0].Content), &metadata); err != nil {
			http.Error(w, "Invalid profile data", http.StatusInternalServerError)
			return
		}

		pictureURL := metadata.Picture
		if pictureURL == "" {
			http.Error(w, "No profile picture", http.StatusNotFound)
			return
		}

		// Create the media key
		mediaKey := fmt.Sprintf("profile_%s%s",
			pubkey,
			filepath.Ext(pictureURL))

		// Check if we have this cached
		if !storage.Has(mediaKey) {
			// Not cached, fetch it now
			go cacheProfileImage(storage, pubkey, pictureURL, mediaKey)

			// Redirect to the original URL for now
			http.Redirect(w, r, pictureURL, http.StatusTemporaryRedirect)
			return
		}

		// Serve the cached image
		reader, err := storage.Get(mediaKey)
		if err != nil {
			http.Error(w, "Error retrieving cached image", http.StatusInternalServerError)
			return
		}
		defer reader.Close()

		// Determine content type based on file extension
		ext := filepath.Ext(pictureURL)
		contentType := "image/jpeg" // Default
		if ext == ".png" {
			contentType = "image/png"
		} else if ext == ".gif" {
			contentType = "image/gif"
		} else if ext == ".webp" {
			contentType = "image/webp"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
		io.Copy(w, reader)
	})

	// Set up the Khatru relay to handle profile metadata events
	relay.OnEventSaved = append(relay.OnEventSaved, func(ctx context.Context, event *nostr.Event) {
		if event.Kind != 0 {
			return // Only process profile metadata events
		}

		// Parse profile metadata
		var metadata ProfileMetadata
		if err := json.Unmarshal([]byte(event.Content), &metadata); err != nil {
			return
		}

		if metadata.Picture == "" {
			return
		}

		// Create a key for this profile picture
		mediaKey := fmt.Sprintf("profile_%s%s",
			event.PubKey,
			filepath.Ext(metadata.Picture))

		// Check if we already have this image cached
		if storage.Has(mediaKey) {
			return // Already cached
		}

		// Cache the image in the background
		go cacheProfileImage(storage, event.PubKey, metadata.Picture, mediaKey)
	})

	// Combine our handlers with the relay's handlers
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			relay.HandleWebsocket(w, r)
		} else if r.Header.Get("Accept") == "application/nostr+json" {
			relay.HandleNIP11(w, r)
		} else {
			mux.ServeHTTP(w, r)
		}
	})

	// Start the server
	log.Printf("Starting Profile Picture Cache Relay on %s", config.ListenAddr)
	log.Fatal(http.ListenAndServe(config.ListenAddr, nil))
}

// fetchAndCacheProfiles fetches and caches profile pictures for a list of pubkeys
func fetchAndCacheProfiles(relay *khatru.Relay, storage *blossom.FilesystemStorage, pubkeys []string, config Config) {
	// Limit concurrent fetches
	semaphore := make(chan struct{}, config.MaxConcurrent)
	
	for _, pubkey := range pubkeys {
		semaphore <- struct{}{} // Acquire semaphore
		
		go func(pk string) {
			defer func() { <-semaphore }() // Release semaphore
			
			// Find the latest profile for this pubkey
			filter := nostr.Filter{
				Kinds:   []int{0},
				Authors: []string{pk},
				Limit:   1,
			}

			events, err := queryEvents(relay, filter)
			if err != nil || len(events) == 0 {
				log.Printf("No profile found for %s: %v", pk, err)
				return
			}

			// Parse profile metadata
			var metadata ProfileMetadata
			if err := json.Unmarshal([]byte(events[0].Content), &metadata); err != nil {
				log.Printf("Invalid profile data for %s: %v", pk, err)
				return
			}

			if metadata.Picture == "" {
				return
			}

			// Create a key for this profile picture
			mediaKey := fmt.Sprintf("profile_%s%s",
				pk,
				filepath.Ext(metadata.Picture))

			// Check if we already have this image cached
			if storage.Has(mediaKey) {
				return // Already cached
			}

			// Cache the image
			cacheProfileImage(storage, pk, metadata.Picture, mediaKey)
		}(pubkey)
	}

	// Wait for all goroutines to finish
	for i := 0; i < config.MaxConcurrent; i++ {
		semaphore <- struct{}{}
	}
}

// cacheProfileImage downloads and caches a profile image
func cacheProfileImage(storage *blossom.FilesystemStorage, pubkey, pictureURL, mediaKey string) {
	log.Printf("Caching profile picture for %s: %s", pubkey, pictureURL)

	// Download the image
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get(pictureURL)
	if err != nil {
		log.Printf("Error downloading image for %s: %v", pubkey, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Bad status code for %s: %d", pubkey, resp.StatusCode)
		return
	}

	// Store the image in Blossom
	if err := storage.Store(mediaKey, resp.Body); err != nil {
		log.Printf("Error storing image for %s: %v", pubkey, err)
		return
	}

	log.Printf("Successfully cached profile picture for %s", pubkey)
}

// queryEvents is a helper function to query events from the relay
func queryEvents(relay *khatru.Relay, filter nostr.Filter) ([]*nostr.Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventsChan, err := relay.QueryEvents[0](ctx, filter)
	if err != nil {
		return nil, err
	}

	var events []*nostr.Event
	for event := range eventsChan {
		events = append(events, event)
	}

	return events, nil
}
