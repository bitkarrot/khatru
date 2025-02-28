# Khatru Profile Picture Cache Relay

A Nostr relay that caches profile pictures and serves them efficiently.

This relay is built on top of [Khatru](https://github.com/fiatjaf/khatru) and supports configurable upstream relays for fetching profile metadata.

## Features

- Caches profile pictures from Nostr metadata (kind 0 events)
- Serves cached images directly
- Fetches profiles from configurable upstream relays if not found locally
- Supports batch caching of profile pictures
- Includes an example client for testing

## API Endpoints

### Profile Picture

```
GET /profile-pic/{pubkey}
```

Fetches and serves the profile picture for the given pubkey. If the profile picture is already cached, it will be served directly. Otherwise, it will be fetched from upstream relays, cached, and then served.

### Batch Cache

```
POST /batch-cache
```

Accepts a JSON payload with a list of pubkeys to cache profile pictures for:

```json
{
  "pubkeys": ["pubkey1", "pubkey2", "pubkey3", ...]
}
```

This endpoint returns immediately and processes the caching in the background.

### Cache Follows

```
GET /cache-follows/{pubkey}?limit=500
```

Fetches the contact list (follows) of the given pubkey and caches profile pictures for all the follows. The `limit` parameter controls the maximum number of follows to process (default: 500, max: 1000).

This endpoint returns immediately and processes the caching in the background.

## Configuration

The relay can be configured via a `config.json` file:

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
