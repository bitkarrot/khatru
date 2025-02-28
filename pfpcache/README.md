# Khatru Profile Picture Cache Relay

A Nostr relay that caches profile pictures for fast access. It stores metadata events (kind 0) and serves profile pictures from a local cache.

## Features

- Caches profile pictures from metadata events
- Serves profile pictures via HTTP
- Configurable upstream relays for fetching profiles
- Batch caching of profile pictures
- Cache follows of a user (contact list)
- Cache purging endpoints

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

Starts a background job to cache profile pictures for the given pubkeys.

#### Cache Follows

```
GET /cache-follows/{pubkey}?limit=100
```

Fetches the follows (contact list) for the given pubkey and caches their profile pictures. The `limit` parameter is optional and defaults to 500.

#### Purge Cache

```
POST /purge-cache/all
POST /purge-cache/profile-pics
```

Purges all cached media or just profile pictures.

#### Purge Single Profile Picture

```
POST /purge-profile-pic/{pubkey}
```

Purges the cached profile picture for the given pubkey.

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
  "cache_expiration_days": 7
}
```

## Running

```bash
./run.sh
```

This will build and start the relay on http://localhost:8080.

## Example Client

An example client is included to demonstrate how to use the profile picture endpoint. Open http://localhost:8080/profile-pic-example.html in your browser to try it out.
