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
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
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
		UpstreamRelays:    []string{"wss://damus.io", "wss://primal.net", "wss://nos.lol"},
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
		// Extract pubkey from path
		pubkey := strings.TrimPrefix(r.URL.Path, "/profile-pic/")
		if pubkey == "" {
			http.Error(w, "Missing pubkey", http.StatusBadRequest)
			return
		}

		// Try to normalize the pubkey if it's in npub format
		if strings.HasPrefix(pubkey, "npub") {
			_, decoded, err := nip19.Decode(pubkey)
			if err == nil {
				pubkey = decoded.(string)
			}
		}

		// Find the latest profile for this pubkey
		filter := nostr.Filter{
			Kinds:   []int{0},
			Authors: []string{pubkey},
			Limit:   1,
		}

		events, err := queryEvents(relay, filter)
		if err != nil || len(events) == 0 {
			// Not found locally, try fetching from public relays
			log.Printf("Profile not found locally for %s, trying public relays", pubkey)
			metadata, err := fetchProfileFromPublicRelays(pubkey)
			if err != nil {
				http.Error(w, "Profile not found", http.StatusNotFound)
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

			// Cache the image in the background
			go cacheProfileImage(storage, pubkey, pictureURL, mediaKey)

			// Redirect to the original URL for now
			http.Redirect(w, r, pictureURL, http.StatusTemporaryRedirect)
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
		// Cache for the number of days specified in config
		cacheSeconds := config.CacheExpirationDays * 24 * 60 * 60
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheSeconds))
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
func cacheProfileImage(storage *FilesystemStorage, pubkey, pictureURL, mediaKey string) {
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

	// Store the image in our storage
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

// fetchProfileFromPublicRelays fetches a profile from public relays
func fetchProfileFromPublicRelays(pubkey string) (*ProfileMetadata, error) {
	// Get the configuration
	config, err := LoadConfig("./config.json")
	if err != nil {
		log.Printf("Error loading config, using default relays: %v", err)
		// Use default relays if config can't be loaded
		config = &Config{
			UpstreamRelays: []string{"wss://damus.io", "wss://primal.net", "wss://nos.lol"},
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
	evts := pool.QuerySync(ctx, config.UpstreamRelays, filter)
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
	for _, storeFunc := range relay.StoreEvent {
		if err := storeFunc(ctx, evts[0]); err != nil {
			log.Printf("Error storing event: %v", err)
		}
	}

	return &metadata, nil
}
