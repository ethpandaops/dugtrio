# Token Router — Per-Client Static Tokens with Per-Path Metrics

## Overview

Add per-customer static token authentication to dugtrio. Each customer gets a unique URL prefix containing their token. Dugtrio validates the token, strips it from the path, and proxies the request through the existing backend pool with full failover. Requests are tracked per customer and per beacon API path via Prometheus metrics.

## Goals

- Unique URL per customer: `https://dugtrio.example.com/<token>/eth/v1/...`
- Static tokens configured in `config.yaml` (no restart-free mutations for now)
- All customers share the same upstream pool and global settings (rate limits, blocked paths)
- Prometheus metrics broken down by billing code × normalized beacon API path × HTTP method × status
- Raw tokens never appear in logs — only `billing_code` is used as the log/metrics identifier

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
      name: "Acme Corp"
      billing_code: "ACM-ETH-01"
    - token: "xyz789ghi012"
      name: "Globex"
      billing_code: "GLB-ETH-01"
```

Fields:
- `token` — secret embedded in the URL path; never logged
- `name` — human-readable display label (used in frontend UI)
- `billing_code` — structured ops identifier (format `AAA-BBB-NN`); used in all log fields and metric labels

New Go types added to `types/config.go`:

```go
type ClientConfig struct {
    Token       string `yaml:"token"`
    Name        string `yaml:"name"`
    BillingCode string `yaml:"billing_code"`
}
```

`ClientConfig` slice added to `ProxyConfig`:

```go
Clients []*ClientConfig `yaml:"clients"`
```

Two additional flags control migration mode:

```yaml
proxy:
  require_tokens: false   # default; set true to enforce token-only access
  clients:
    - token: "abc123def456"
      name: "Acme Corp"
      billing_code: "ACM-ETH-01"
```

- `require_tokens: false` (default) — token URLs work alongside existing direct access (`/eth/v1/...`); zero breaking change for current clients
- `require_tokens: true` — direct `/eth/`, `/lighthouse/`, etc. routes return `401 Unauthorized`; only token-prefixed URLs are accepted

At startup, `TokenRouter` builds `map[string]*types.ClientConfig` (token → config) for O(1) lookup. Raw token strings never appear in logs or metric labels — `BillingCode` is used everywhere a machine-readable identifier is needed; `Name` is display-only.

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
  4. Store billing_code on request context (unexported key) — never the raw token
  5. Delegate to beaconProxy.processCall(w, r, UnspecifiedClient)
```

### HTTP registration

Registered in `main.go` using gorilla/mux, **after** all specific prefix routes (`/eth/`, `/lighthouse/`, `/metrics`, etc.) and **before** the frontend catch-all:

```go
router.PathPrefix("/{token}/").Handler(tokenRouter)
```

Because gorilla/mux matches in registration order, all known prefixes take priority. The `/{token}/` wildcard only fires for paths that haven't already matched. `TokenRouter.ServeHTTP` then validates the captured segment against the token map and returns 401 if unknown.

When `require_tokens: false` (default), the existing `/eth/` and client-specific prefix routes remain fully functional — no changes for current clients. When `require_tokens: true`, a guard middleware wraps those routes and returns `401` before they are reached.

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
- `billing_code` — token's `BillingCode` field (e.g. `ACM-ETH-01`); empty string for Basic Auth sessions
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

The counter is incremented in a deferred call inside `BeaconProxy.processCall`, after the response is committed. The `billing_code` is read from the request context. If absent (Basic Auth path), the label is set to `""` so existing sessions are unaffected.

### Exposure

Existing `/metrics` Prometheus endpoint — no new endpoint required.

---

## File Changes Summary

| File | Change |
|---|---|
| `types/config.go` | Add `ClientConfig`, add `Clients []*ClientConfig` and `RequireTokens bool` to `ProxyConfig` |
| `proxy/tokenrouter.go` | New — `TokenRouter` struct and `ServeHTTP` |
| `proxy/pathutil.go` | New — `NormalizePath` function |
| `proxy/beaconproxy.go` | Read client name from context, increment metric in `processCall` |
| `metrics/metrics.go` | Add `ClientRequests` counter vec |
| `cmd/dugtrio-proxy/main.go` | Register `/{token}/` route; wrap legacy routes with guard when `require_tokens: true` |

---

## Challenges & Notes

- **Path normalization completeness** — the Beacon API has ~80+ paths; the normalizer only needs to cover paths that are actually called, so it can grow incrementally. Unrecognized paths are safe (just high-cardinality if called with many unique IDs).
- **Token masking** — `TokenRouter` strips the token from `r.URL.Path` before any log statement or upstream request. All log fields and metric labels use `billing_code` exclusively. The raw token only exists in memory at the moment of lookup and is never forwarded or recorded. Operators should still configure their reverse proxy (nginx/Caddy) to redact the first URL path segment if access logs are retained.
- **Basic Auth coexistence** — both auth mechanisms are independent code paths. Operators can run both during migration or use one exclusively.

---

## Rollout Plan

Migration from direct access to token-prefixed URLs in three phases, each independently deployable.

### Phase 1 — Deploy with tokens, direct access still open

`require_tokens: false` (default). Add `proxy.clients` to config and redeploy.

- Existing clients are unaffected — all direct `/eth/v1/...` calls continue to work
- New token URLs become available immediately
- Metrics start recording per-`billing_code` breakdowns for token-using clients
- Goal: validate that token routing and metrics work in production before touching existing clients

### Phase 2 — Migrate clients to token URLs

Distribute a token URL to each client and ask them to update their endpoint config. No dugtrio change required.

- Run Phase 1 and Phase 2 in parallel: some clients on token URLs, some still on direct access
- `billing_code` metrics give visibility into which clients have migrated (non-empty label) vs. not (empty label)
- Direct access remains fully functional during this window

### Phase 3 — Enforce token-only access

Once all clients confirm they are on token URLs, flip `require_tokens: true` and redeploy.

- Legacy routes (`/eth/`, `/lighthouse/`, etc.) return `401` for unauthenticated requests
- Any client that missed migration gets an immediate, clear error (not a silent failure)
- Basic Auth (`proxy.auth`) is unaffected if still in use alongside token auth

**Rollback at any phase:** set `require_tokens: false` (or remove it entirely) and redeploy — zero data loss, zero downtime.
