package main

import (
	"bytes"
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
	ListenAddr        string   `json:"listen_addr"`
	DatabasePath      string   `json:"database_path"`
	MediaCachePath    string   `json:"media_cache_path"`
	UpstreamRelays    []string `json:"upstream_relays"`
	MaxConcurrent     int      `json:"max_concurrent"`
	CacheExpirationDays int    `json:"cache_expiration_days"`
}

// LoadConfig loads the configuration from a file
func LoadConfig(configPath string) (*Config, error) {
	// Default configuration
	config := &Config{
		ListenAddr:        ":8080",
		DatabasePath:      "./data/pfpcache.db",
		MediaCachePath:    "./data/media_cache",
		UpstreamRelays:    []string{"wss://damus.io", "wss://primal.net", "wss://nos.lol", "wss://purplepag.es"},
		MaxConcurrent:     20,
		CacheExpirationDays: 7,
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("Config file not found at %s, using defaults", configPath)
		// Save the default config for future use
		configJSON, _ := json.MarshalIndent(config, "", "  ")
		if err := os.WriteFile(configPath, configJSON, 0644); err != nil {
			log.Printf("Failed to write default config: %v", err)
		}
		return config, nil
	}

	// Read config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	// Parse config
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	log.Printf("Loaded configuration from %s", configPath)
	return config, nil
}

// FilesystemStorage implements a simple filesystem-based storage for profile pictures
type FilesystemStorage struct {
	basePath string
}

// NewFilesystemStorage creates a new filesystem storage
func NewFilesystemStorage(basePath string) *FilesystemStorage {
	return &FilesystemStorage{
		basePath: basePath,
	}
}

// Store saves a file to the filesystem
func (fs *FilesystemStorage) Store(key string, reader io.Reader) error {
	filePath := filepath.Join(fs.basePath, key)
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	
	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// Copy the content
	_, err = io.Copy(file, reader)
	return err
}

// Get retrieves a file from the filesystem
func (fs *FilesystemStorage) Get(key string) (io.ReadCloser, error) {
	filePath := filepath.Join(fs.basePath, key)
	return os.Open(filePath)
}

// Has checks if a file exists
func (fs *FilesystemStorage) Has(key string) bool {
	filePath := filepath.Join(fs.basePath, key)
	_, err := os.Stat(filePath)
	return err == nil
}

// Delete removes a file
func (fs *FilesystemStorage) Delete(key string) error {
	filePath := filepath.Join(fs.basePath, key)
	return os.Remove(filePath)
}

// MediaHandler handles HTTP requests for media files
type MediaHandler struct {
	storage *FilesystemStorage
	config  *Config
}

// NewMediaHandler creates a new media handler
func NewMediaHandler(storage *FilesystemStorage, config *Config) *MediaHandler {
	return &MediaHandler{
		storage: storage,
		config:  config,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *MediaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	key := r.URL.Path
	if key == "" {
		http.Error(w, "Missing key", http.StatusBadRequest)
		return
	}
	
	// Check if the file exists
	if !h.storage.Has(key) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	
	// Get the file
	reader, err := h.storage.Get(key)
	if err != nil {
		http.Error(w, "Error retrieving file", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	
	// Determine content type based on file extension
	ext := filepath.Ext(key)
	contentType := "application/octet-stream" // Default
	if ext == ".jpg" || ext == ".jpeg" {
		contentType = "image/jpeg"
	} else if ext == ".png" {
		contentType = "image/png"
	} else if ext == ".gif" {
		contentType = "image/gif"
	} else if ext == ".webp" {
		contentType = "image/webp"
	}
	
	w.Header().Set("Content-Type", contentType)
	// Cache for the number of days specified in config
	cacheSeconds := h.config.CacheExpirationDays * 24 * 60 * 60
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheSeconds))
	io.Copy(w, reader)
}

func main() {
	// Load configuration
	configPath := "./config.json"
	config, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
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

	// Initialize filesystem storage for media
	storage := NewFilesystemStorage(config.MediaCachePath)
	mediaHandler := NewMediaHandler(storage, config)

	// Create a mux for our HTTP handlers
	mux := http.NewServeMux()

	// Serve static files (including client.html)
	mux.Handle("/", http.FileServer(http.Dir(".")))

	// Register the media handler for media serving
	mux.Handle("/media/", http.StripPrefix("/media/", mediaHandler))

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
		go fetchAndCacheProfiles(relay, storage, request.Pubkeys, *config)

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
		// Extract the pubkey from the URL
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 3 {
			http.Error(w, "Invalid URL", http.StatusBadRequest)
			return
		}
		pubkey := parts[2]

		// Check if we have this profile in our database
		events, err := queryEvents(relay, nostr.Filter{
			Kinds:   []int{0},
			Authors: []string{pubkey},
			Limit:   1,
		})

		var event *nostr.Event
		if err != nil || len(events) == 0 {
			// Profile not found locally, try public relays
			log.Printf("Profile not found locally for %s, trying public relays", pubkey)
			metadata, err := fetchProfileFromPublicRelays(pubkey)
			if err != nil {
				http.Error(w, fmt.Sprintf("Profile not found: %v", err), http.StatusNotFound)
				return
			}

			// Check if we already have this image cached
			cacheKey := fmt.Sprintf("profile-pic/%s", pubkey)
			if storage.Has(cacheKey) {
				log.Printf("Serving cached profile picture for %s", pubkey)
				reader, err := storage.Get(cacheKey)
				if err != nil {
					http.Error(w, "Error retrieving cached image", http.StatusInternalServerError)
					return
				}
				defer reader.Close()

				// Determine content type based on file extension
				ext := filepath.Ext(metadata.Picture)
				contentType := "image/jpeg" // Default
				if ext == ".png" {
					contentType = "image/png"
				} else if ext == ".gif" {
					contentType = "image/gif"
				} else if ext == ".webp" {
					contentType = "image/webp"
				}

				w.Header().Set("Content-Type", contentType)
				// Cache for the number of days specified in config
				cacheSeconds := config.CacheExpirationDays * 24 * 60 * 60
				w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheSeconds))
				io.Copy(w, reader)
				return
			}

			// Cache the image in the background
			go cacheProfileImage(storage, pubkey, metadata.Picture)

			// Redirect to the original URL for now
			http.Redirect(w, r, metadata.Picture, http.StatusTemporaryRedirect)
			return
		} else {
			event = events[0]
		}

		// Parse profile metadata
		var metadata ProfileMetadata
		if err := json.Unmarshal([]byte(event.Content), &metadata); err != nil {
			http.Error(w, "Invalid profile data", http.StatusInternalServerError)
			return
		}

		pictureURL := metadata.Picture
		if pictureURL == "" {
			http.Error(w, "No profile picture found", http.StatusNotFound)
			return
		}

		// Check if we already have this image cached
		cacheKey := fmt.Sprintf("profile-pic/%s", pubkey)
		if storage.Has(cacheKey) {
			log.Printf("Serving cached profile picture for %s", pubkey)
			reader, err := storage.Get(cacheKey)
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
			// Cache for the number of days specified in config
			cacheSeconds := config.CacheExpirationDays * 24 * 60 * 60
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheSeconds))
			io.Copy(w, reader)
			return
		}

		// Fetch and cache the image
		log.Printf("Fetching profile picture for %s from %s", pubkey, pictureURL)
		resp, err := http.Get(pictureURL)
		if err != nil {
			log.Printf("Error fetching profile picture: %v", err)
			// If we can't fetch the image, redirect to the original URL
			http.Redirect(w, r, pictureURL, http.StatusTemporaryRedirect)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("Error fetching profile picture, status: %d", resp.StatusCode)
			// If we can't fetch the image, redirect to the original URL
			http.Redirect(w, r, pictureURL, http.StatusTemporaryRedirect)
			return
		}

		// Cache the image
		var buf bytes.Buffer
		tee := io.TeeReader(resp.Body, &buf)
		if err := storage.Store(cacheKey, tee); err != nil {
			log.Printf("Error caching profile picture: %v", err)
			// If we can't cache the image, redirect to the original URL
			http.Redirect(w, r, pictureURL, http.StatusTemporaryRedirect)
			return
		}

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
		// Cache for the number of days specified in config
		cacheSeconds := config.CacheExpirationDays * 24 * 60 * 60
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheSeconds))
		io.Copy(w, &buf)
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
		cacheKey := fmt.Sprintf("profile-pic/%s", event.PubKey)

		// Check if we already have this image cached
		if storage.Has(cacheKey) {
			return // Already cached
		}

		// Cache the image in the background
		go cacheProfileImage(storage, event.PubKey, metadata.Picture)
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

	// Set the global relay instance
	setRelayInstance(relay)

	// Start the server
	log.Printf("Starting Profile Picture Cache Relay on %s", config.ListenAddr)
	log.Fatal(http.ListenAndServe(config.ListenAddr, nil))
}

// cacheProfileImage caches a profile image
func cacheProfileImage(storage *FilesystemStorage, pubkey, pictureURL string) error {
	log.Printf("Caching profile picture for %s: %s", pubkey, pictureURL)
	
	// Create the cache key
	cacheKey := fmt.Sprintf("profile-pic/%s", pubkey)
	
	// Check if we already have this image cached
	if storage.Has(cacheKey) {
		log.Printf("Profile picture for %s already cached", pubkey)
		return nil
	}
	
	// Fetch the image
	resp, err := http.Get(pictureURL)
	if err != nil {
		return fmt.Errorf("error fetching image: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error fetching image, status: %d", resp.StatusCode)
	}
	
	// Store the image
	if err := storage.Store(cacheKey, resp.Body); err != nil {
		return fmt.Errorf("error storing image: %v", err)
	}
	
	log.Printf("Successfully cached profile picture for %s", pubkey)
	return nil
}

// fetchAndCacheProfiles fetches and caches profile pictures for a list of pubkeys
func fetchAndCacheProfiles(relay *khatru.Relay, storage *FilesystemStorage, pubkeys []string, config Config) {
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

			// Cache the image
			if err := cacheProfileImage(storage, pk, metadata.Picture); err != nil {
				log.Printf("Error caching profile picture for %s: %v", pk, err)
			}
		}(pubkey)
	}

	// Wait for all goroutines to finish
	for i := 0; i < config.MaxConcurrent; i++ {
		semaphore <- struct{}{}
	}
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

// fetchProfileFromPublicRelays fetches a profile from public relays
func fetchProfileFromPublicRelays(pubkey string) (*ProfileMetadata, error) {
	// Get the configuration
	config, err := LoadConfig("./config.json")
	if err != nil {
		log.Printf("Error loading config, using default relays: %v", err)
		// Use default relays if config can't be loaded
		config = &Config{
			UpstreamRelays: []string{"wss://damus.io", "wss://primal.net", "wss://nos.lol", "wss://purplepag.es"},
		}
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect to relays
	pool := nostr.NewSimplePool(ctx)

	// Query for the profile
	filter := nostr.Filter{
		Kinds:   []int{0},
		Authors: []string{pubkey},
		Limit:   1,
	}

	log.Printf("Querying public relays for profile of %s", pubkey)
	
	// Use the proper method for the current version of go-nostr
	filters := nostr.Filters{filter}
	ch := pool.SubMany(ctx, config.UpstreamRelays, filters)
	var evts []*nostr.Event
	for ev := range ch {
		if ev.Event != nil {
			evts = append(evts, ev.Event)
		}
	}
	
	if len(evts) == 0 {
		return nil, fmt.Errorf("no profile found on public relays")
	}

	// Parse the profile metadata
	var metadata ProfileMetadata
	if err := json.Unmarshal([]byte(evts[0].Content), &metadata); err != nil {
		return nil, err
	}

	// Store the event in our local database
	log.Printf("Storing profile for %s in local database", pubkey)
	
	// Get the relay instance from the global scope
	relay, err := getRelayInstance()
	if err != nil {
		log.Printf("Error getting relay instance: %v", err)
	} else {
		for _, storeFunc := range relay.StoreEvent {
			if err := storeFunc(ctx, evts[0]); err != nil {
				log.Printf("Error storing event: %v", err)
			}
		}
	}

	return &metadata, nil
}

// Global relay instance for access from any function
var globalRelay *khatru.Relay

// setRelayInstance sets the global relay instance
func setRelayInstance(relay *khatru.Relay) {
	globalRelay = relay
}

// getRelayInstance gets the global relay instance
func getRelayInstance() (*khatru.Relay, error) {
	if globalRelay == nil {
		return nil, fmt.Errorf("relay instance not set")
	}
	return globalRelay, nil
}
