# Khatru Profile Picture Cache Relay

A Nostr relay that caches profile pictures for fast access. It stores metadata events (kind 0) and serves profile pictures from a local cache.

## Features

- Caches profile pictures from metadata events
- Serves profile pictures via HTTP
- Configurable upstream relays for fetching profiles
- Batch caching of profile pictures
- Cache follows of a user (contact list)
- Cache purging endpoints
- LRU (Least Recently Used) cache management

## Usage

### Starting the Relay

```bash
./run.sh
```

### Endpoints

#### Profile Picture

```
GET /profile-pic/{pubkey}
```

Returns the profile picture for the given pubkey. If the profile picture is not cached, it will be fetched from the upstream relays and cached.

**Example:**

```html
<!-- In HTML -->
<img src="http://localhost:8080/profile-pic/32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245" alt="Profile picture">
```

```bash
# Using curl
curl -o profile.jpg http://localhost:8080/profile-pic/32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245
```

#### Batch Cache

```
POST /batch-cache
```

Request body:
```json
{
  "pubkeys": ["pubkey1", "pubkey2", ...]
}
```

Starts a background job to cache profile pictures for the given pubkeys. Returns immediately with a status message.

**Example:**

```bash
# Using curl
curl -X POST -H "Content-Type: application/json" -d '{"pubkeys": ["32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245", "3878d95db7b854c3a0d3b2d6b7bf9bf28b36162be64326f5521ba71cf3b45a69"]}' http://localhost:8080/batch-cache
```

**Response:**

```json
{
  "message": "Started caching 2 profile pictures",
  "count": 2
}
```

#### Cache Follows

```
GET /cache-follows/{pubkey}?limit=100
```

Fetches the follows (contact list) for the given pubkey and caches their profile pictures. The `limit` parameter is optional and defaults to 500.

**Example:**

```bash
# Cache profile pictures for up to 100 follows of a user
curl -X GET http://localhost:8080/cache-follows/32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245?limit=100
```

**Response:**

```json
{
  "message": "Started caching profile pictures for 100 follows",
  "count": 100,
  "pubkey": "32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245"
}
```

#### Purge Cache

```
POST /purge-cache/all
POST /purge-cache/profile-pics
```

Purges all cached media or just profile pictures.

**Examples:**

```bash
# Purge all cached media
curl -X POST http://localhost:8080/purge-cache/all

# Purge only profile pictures
curl -X POST http://localhost:8080/purge-cache/profile-pics
```

**Response:**

```json
{
  "status": "success",
  "message": "Profile picture cache purged successfully"
}
```

#### Purge Single Profile Picture

```
POST /purge-profile-pic/{pubkey}
```

Purges the cached profile picture for the given pubkey.

**Example:**

```bash
# Purge a specific profile picture
curl -X POST http://localhost:8080/purge-profile-pic/32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245
```

**Response:**

```json
{
  "status": "success",
  "message": "Profile picture for 32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245 purged successfully"
}
```

## Configuration

The relay can be configured via the `config.json` file:

```json
{
  "listen_addr": ":8080",
  "database_path": "./data/pfpcache.db",
  "media_cache_path": "./data/media_cache",
  "upstream_relays": [
    "wss://damus.io",
    "wss://primal.net",
    "wss://nos.lol",
    "wss://purplepag.es"
  ],
  "max_concurrent": 20,
  "cache_expiration_days": 30,
  "max_cache_size_mb": 1024,
  "lru_check_interval": 60
}
```

- `listen_addr`: The address to listen on
- `database_path`: Path to the SQLite database file
- `media_cache_path`: Path to the directory where media files are cached
- `upstream_relays`: List of Nostr relays to fetch profiles from
- `max_concurrent`: Maximum number of concurrent profile fetches
- `cache_expiration_days`: Number of days to cache profile pictures for
- `max_cache_size_mb`: Maximum size of the cache in megabytes (LRU cache will remove least recently used files when this limit is exceeded)
- `lru_check_interval`: Interval in minutes to check and clean the LRU cache

## Running

```bash
./run.sh
```

This will build and start the relay on http://localhost:8080.

## Example Client

See `profile-pic-example.html` for an example of how to use the profile picture endpoint in a web page.

## LRU Cache Management

The relay implements a Least Recently Used (LRU) cache mechanism to automatically manage the cache size:

- The cache size is limited by the `max_cache_size_mb` configuration parameter
- The system tracks when each file was last accessed
- When the cache size exceeds the limit, the least recently used files are removed first
- The cache is checked periodically based on the `lru_check_interval` configuration parameter

This ensures that:
1. The cache doesn't grow indefinitely
2. The most frequently accessed profile pictures remain in the cache
3. Older, unused profile pictures are automatically removed

You can still manually purge the cache using the purge endpoints if needed.

## Use Cases

### 1. Fast Profile Picture Loading in Web Apps

Web applications can use the profile picture endpoint to quickly load profile pictures without having to query Nostr relays directly:

```javascript
// Example JavaScript
function loadProfilePicture(pubkey) {
  const img = document.createElement('img');
  img.src = `http://localhost:8080/profile-pic/${pubkey}`;
  img.alt = 'Profile picture';
  document.getElementById('profile-container').appendChild(img);
}
```

### 2. Preloading Profile Pictures for a Feed

Before displaying a feed of posts, you can preload all the profile pictures:

```javascript
// Example JavaScript
async function preloadProfilePictures(pubkeys) {
  const response = await fetch('http://localhost:8080/batch-cache', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ pubkeys }),
  });
  
  console.log('Preloading profile pictures:', await response.json());
}
```

### 3. Caching Profile Pictures for a User's Network

When a user logs in, cache profile pictures for all their follows:

```javascript
// Example JavaScript
async function cacheNetworkProfilePics(userPubkey) {
  const response = await fetch(`http://localhost:8080/cache-follows/${userPubkey}`);
  console.log('Caching network profile pictures:', await response.json());
}
```

### 4. Clearing Cache During Development

During development or testing, you might want to clear the cache:

```bash
# Clear all cached media
curl -X POST http://localhost:8080/purge-cache/all

# Clear only profile pictures
curl -X POST http://localhost:8080/purge-cache/profile-pics
