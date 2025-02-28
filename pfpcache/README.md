# Profile Picture Cache Relay

A specialized Nostr relay that caches profile pictures using Khatru and Blossom Media Storage.

## Features

1. **Batch Processing**: Request caching for up to 500 follows at once
2. **Non-blocking**: Caching happens asynchronously without blocking the UI
3. **Progressive Loading**: Images can be displayed immediately, with caching happening in the background
4. **Persistent Cache**: Blossom storage ensures images remain cached between sessions
5. **Flexible URL Scheme**: The `/profile-pic/{pubkey}` endpoint makes it easy to reference images without needing to know the exact URL

## How It Works

This relay uses Khatru as the foundation and Blossom for media storage. It provides:

1. A Nostr relay that subscribes to profile metadata events (kind 0)
2. A media storage system that caches profile pictures
3. HTTP endpoints for batch caching and retrieving profile pictures

## Endpoints

- **WebSocket**: Standard Nostr relay WebSocket endpoint at `/`
- **NIP-11**: Standard Nostr relay information document at `/` with `Accept: application/nostr+json` header
- **Profile Picture**: Get a profile picture at `/profile-pic/{pubkey}`
- **Batch Cache**: Request caching of multiple profiles at `/cache-profiles` (POST)

## Usage

### Starting the Relay

```bash
cd pfpcache
go run main.go
```

The relay will start on port 8080 by default.

### Caching Profile Pictures

To request caching of multiple profiles, send a POST request to `/cache-profiles`:

```javascript
// Example using fetch
async function cacheProfileImages(pubkeys) {
  const response = await fetch('http://localhost:8080/cache-profiles', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ pubkeys })
  });
  
  return await response.json();
}

// Example usage
cacheProfileImages([
  'pubkey1',
  'pubkey2',
  // ... up to 500 pubkeys
]);
```

### Retrieving Profile Pictures

To get a profile picture, use the `/profile-pic/{pubkey}` endpoint:

```html
<img src="http://localhost:8080/profile-pic/pubkey1" alt="Profile picture" />
```

If the image is already cached, it will be served directly. If not, it will be fetched and cached in the background, and the request will be redirected to the original URL.

## Configuration

The relay can be configured by modifying the `Config` struct in `main.go`:

- `ListenAddr`: The address to listen on (default: `:8080`)
- `DatabasePath`: Path to the SQLite database (default: `./data/pfpcache.db`)
- `MediaCachePath`: Path to the media cache directory (default: `./data/media_cache`)
- `UpstreamRelays`: List of upstream relays to connect to
- `MaxConcurrent`: Maximum number of concurrent image downloads (default: 20)
- `CacheExpiration`: How long to cache images (default: 7 days)

## Implementation Details

- Uses SQLite for storing Nostr events
- Uses Blossom's filesystem storage for caching images
- Implements rate limiting for concurrent downloads
- Handles content type detection for different image formats
- Implements error handling and logging
