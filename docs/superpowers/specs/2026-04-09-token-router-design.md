# Token Router — Per-Client Static Tokens with Per-Path Metrics

## Overview

Add per-customer static token authentication to dugtrio. Each customer gets a unique URL prefix containing their token. Dugtrio validates the token, strips it from the path, and proxies the request through the existing backend pool with full failover. Requests are tracked per customer and per beacon API path via Prometheus metrics.

## Goals

- Unique URL per customer: `https://dugtrio.example.com/<token>/eth/v1/...`
- Static tokens configured in `config.yaml` (no restart-free mutations for now)
- All customers share the same upstream pool and global settings (rate limits, blocked paths)
- Prometheus metrics broken down by customer name × normalized beacon API path × HTTP method × status

## Non-Goals

- Per-token rate limits (future — 429 groundwork noted)
- Per-token backend routing (future — all tokens share the same pool)
- Dynamic token management API (future)
- Removing or replacing existing Basic Auth (stays untouched)

---

## Config

New `clients` list under `proxy`:

```yaml
proxy:
  clients:
    - token: "abc123def456"
      name: "customer-a"
    - token: "xyz789ghi012"
      name: "customer-b"
```

New Go types added to `types/config.go`:

```go
type ClientConfig struct {
    Token string `yaml:"token"`
    Name  string `yaml:"name"`
}
```

`ClientConfig` slice added to `ProxyConfig`:

```go
Clients []*ClientConfig `yaml:"clients"`
```

At startup, `TokenRouter` builds `map[string]*types.ClientConfig` (token → config) for O(1) lookup. Raw token strings never appear in logs or metric labels — only `Name` is used.

---

## TokenRouter

New file: `proxy/tokenrouter.go`

```go
type TokenRouter struct {
    beaconProxy *BeaconProxy
    tokens      map[string]*types.ClientConfig
}
```

### Request flow

```
GET /<token>/eth/v1/beacon/blocks/head
         ↓
TokenRouter.ServeHTTP
  1. Extract first path segment as token candidate
  2. Lookup token in map → 401 Unauthorized if not found
  3. Strip token prefix → r.URL.Path = /eth/v1/beacon/blocks/head
  4. Store client name on request context (unexported key)
  5. Delegate to beaconProxy.processCall(w, r, UnspecifiedClient)
```

### HTTP registration

Registered in `main.go` using gorilla/mux, **after** all specific prefix routes (`/eth/`, `/lighthouse/`, `/metrics`, etc.) and **before** the frontend catch-all:

```go
router.PathPrefix("/{token}/").Handler(tokenRouter)
```

Because gorilla/mux matches in registration order, all known prefixes take priority. The `/{token}/` wildcard only fires for paths that haven't already matched. `TokenRouter.ServeHTTP` then validates the captured segment against the token map and returns 401 if unknown.

The existing `/eth/` and client-specific prefix routes remain for Basic Auth users and unauthenticated access.

### Context key

```go
type contextKey struct{}
var clientNameKey = contextKey{}
```

Unexported type prevents collisions with other packages.

### Error responses

| Condition | Status |
|---|---|
| Token missing or invalid | 401 Unauthorized |
| Rate limit exceeded (future) | 429 Too Many Requests |

---

## Per-Token + Per-Path Metrics

### New counter

Added to `metrics/metrics.go`:

```go
ClientRequests *prometheus.CounterVec
// labels: client_name, path, method, status
```

Labels:
- `client_name` — token's `Name` field
- `path` — normalized beacon API path (see below)
- `method` — `GET` or `POST`
- `status` — `success`, `upstream_error`, `blocked`, `no_upstream`

### Path normalization

New function `NormalizePath(path string) string` in `proxy/pathutil.go`. Uses an ordered slice of compiled `*regexp.Regexp` → replacement string pairs covering the standard Beacon API path patterns.

Representative normalizations:

```
/eth/v1/beacon/blocks/12345           → /eth/v1/beacon/blocks/{block_id}
/eth/v1/beacon/blob_sidecars/12345    → /eth/v1/beacon/blob_sidecars/{block_id}
/eth/v1/beacon/states/head/...        → /eth/v1/beacon/states/{state_id}/...
/eth/v1/validator/duties/attester/5   → /eth/v1/validator/duties/attester/{epoch}
/eth/v1/beacon/headers/0xabc...       → /eth/v1/beacon/headers/{block_id}
```

Unknown paths are passed through as-is. Since only registered clients can reach this code path, cardinality is bounded.

### Increment point

The counter is incremented in a deferred call inside `BeaconProxy.processCall`, after the response is committed. The client name is read from the request context. If no client name is present (Basic Auth path), the label is set to `""` so existing sessions are unaffected.

### Exposure

Existing `/metrics` Prometheus endpoint — no new endpoint required.

---

## File Changes Summary

| File | Change |
|---|---|
| `types/config.go` | Add `ClientConfig`, add `Clients []*ClientConfig` to `ProxyConfig` |
| `proxy/tokenrouter.go` | New — `TokenRouter` struct and `ServeHTTP` |
| `proxy/pathutil.go` | New — `NormalizePath` function |
| `proxy/beaconproxy.go` | Read client name from context, increment metric in `processCall` |
| `metrics/metrics.go` | Add `ClientRequests` counter vec |
| `frontend/frontend.go` | Register `/{token}/` route |

---

## Challenges & Notes

- **Path normalization completeness** — the Beacon API has ~80+ paths; the normalizer only needs to cover paths that are actually called, so it can grow incrementally. Unrecognized paths are safe (just high-cardinality if called with many unique IDs).
- **Token security** — tokens in URL paths appear in access logs by default. Operators should configure log redaction or use a reverse proxy (nginx/Caddy) that strips the token before logging if required.
- **Basic Auth coexistence** — both auth mechanisms are independent code paths. Operators can run both during migration or use one exclusively.
