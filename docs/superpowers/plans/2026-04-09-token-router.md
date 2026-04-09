# Token Router Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-customer static token authentication — unique URL prefix per customer, backward-compatible with existing direct access, per-token Prometheus metrics by beacon API path.

**Architecture:** A `TokenRouter` HTTP handler wraps `BeaconProxy` — it validates the token in the URL prefix, strips it, stores `billing_code` on the request context, and delegates to the existing proxy. A `require_tokens` flag optionally blocks legacy direct access routes for Phase 3 migration. A new `dugtrio_billing_requests_total` Prometheus counter tracks requests by `billing_code × normalized_path × method × status`.

**Tech Stack:** Go 1.25, `github.com/gorilla/mux`, `github.com/prometheus/client_golang`, `github.com/stretchr/testify`, `gopkg.in/yaml.v3`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `types/config.go` | Modify | Add `ClientConfig`, `RequireTokens`, `Clients` to `ProxyConfig` |
| `types/config_test.go` | Create | Verify YAML unmarshalling of new fields |
| `proxy/pathutil.go` | Create | `NormalizePath` — strip query string, replace hex/numeric path segments |
| `proxy/pathutil_test.go` | Create | Table-driven tests for path normalization |
| `proxy/tokenrouter.go` | Create | `TokenRouter`, `billingContextKey`, `getBillingCode` |
| `proxy/tokenrouter_test.go` | Create | Unit tests: 401 on invalid token, path stripping, context value |
| `metrics/metrics.go` | Modify | Accept `prometheus.Registerer`, add `billingRequests` counter + `AddBillingCall` |
| `metrics/metrics_test.go` | Create | Verify `AddBillingCall` increments the right label combination |
| `proxy/beaconproxy.go` | Modify | Read `billing_code` from context, defer `AddBillingCall` in `processCall` |
| `cmd/dugtrio-proxy/main.go` | Modify | Register `TokenRouter` route, add `requireTokensGuard` for legacy routes |

---

## Task 1: Config Types

**Files:**
- Modify: `types/config.go`
- Create: `types/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `types/config_test.go`:

```go
package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestProxyConfig_ClientsUnmarshal(t *testing.T) {
	raw := `
proxy:
  require_tokens: true
  clients:
    - token: "abc123"
      name: "Acme Corp"
      billing_code: "ACM-ETH-01"
    - token: "xyz789"
      name: "Globex"
      billing_code: "GLB-ETH-02"
`
	cfg := &Config{}
	err := yaml.Unmarshal([]byte(raw), cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Proxy)
	assert.True(t, cfg.Proxy.RequireTokens)
	require.Len(t, cfg.Proxy.Clients, 2)
	assert.Equal(t, "abc123", cfg.Proxy.Clients[0].Token)
	assert.Equal(t, "Acme Corp", cfg.Proxy.Clients[0].Name)
	assert.Equal(t, "ACM-ETH-01", cfg.Proxy.Clients[0].BillingCode)
	assert.Equal(t, "xyz789", cfg.Proxy.Clients[1].Token)
	assert.Equal(t, "GLB-ETH-02", cfg.Proxy.Clients[1].BillingCode)
}

func TestProxyConfig_DefaultsWhenClientsAbsent(t *testing.T) {
	raw := `proxy: {}`
	cfg := &Config{}
	err := yaml.Unmarshal([]byte(raw), cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Proxy)
	assert.False(t, cfg.Proxy.RequireTokens)
	assert.Empty(t, cfg.Proxy.Clients)
}
```

- [ ] **Step 2: Run test — expect compile error**

```bash
go test ./types/...
```

Expected: compile error — `ClientConfig`, `RequireTokens`, `Clients` undefined.

- [ ] **Step 3: Add types to `types/config.go`**

Add the new struct and fields. In `types/config.go`, after the existing `AuthConfig` type:

```go
type ClientConfig struct {
	Token       string `yaml:"token"`
	Name        string `yaml:"name"`
	BillingCode string `yaml:"billing_code"`
}
```

In `ProxyConfig`, add two fields after `Auth`:

```go
RequireTokens bool            `yaml:"require_tokens" envconfig:"PROXY_REQUIRE_TOKENS"`
Clients       []*ClientConfig `yaml:"clients"`
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./types/...
```

Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
git add types/config.go types/config_test.go
git commit -m "feat: add ClientConfig and RequireTokens to ProxyConfig"
```

---

## Task 2: Path Normalization

**Files:**
- Create: `proxy/pathutil.go`
- Create: `proxy/pathutil_test.go`

- [ ] **Step 1: Write the failing tests**

Create `proxy/pathutil_test.go`:

```go
package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/eth/v1/node/version", "/eth/v1/node/version"},
		{"/eth/v1/beacon/blocks/12345", "/eth/v1/beacon/blocks/{id}"},
		{"/eth/v1/beacon/headers/0xabc123def", "/eth/v1/beacon/headers/{hex}"},
		{"/eth/v1/beacon/blob_sidecars/7890", "/eth/v1/beacon/blob_sidecars/{id}"},
		{"/eth/v1/beacon/states/head/finality_checkpoints", "/eth/v1/beacon/states/head/finality_checkpoints"},
		{"/eth/v1/beacon/states/99/validators/0xabc", "/eth/v1/beacon/states/{id}/validators/{hex}"},
		{"/eth/v1/validator/duties/attester/5", "/eth/v1/validator/duties/attester/{id}"},
		{"/eth/v1/beacon/blocks/12345?format=ssz", "/eth/v1/beacon/blocks/{id}"},
		{"/eth/v1/beacon/blocks/head", "/eth/v1/beacon/blocks/head"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, NormalizePath(tc.input))
		})
	}
}
```

- [ ] **Step 2: Run test — expect compile error**

```bash
go test ./proxy/...
```

Expected: compile error — `NormalizePath` undefined.

- [ ] **Step 3: Implement `proxy/pathutil.go`**

Create `proxy/pathutil.go`:

```go
package proxy

import (
	"strconv"
	"strings"
)

// NormalizePath strips the query string and replaces variable path segments
// (hex identifiers and numeric IDs) with typed placeholders. Named segments
// like "head", "finalized", "genesis" are left as-is.
func NormalizePath(path string) string {
	if q := strings.IndexByte(path, '?'); q != -1 {
		path = path[:q]
	}

	parts := strings.Split(path, "/")

	for i, p := range parts {
		if i < 2 {
			continue
		}

		if strings.HasPrefix(p, "0x") {
			parts[i] = "{hex}"
			continue
		}

		if _, err := strconv.ParseUint(p, 10, 64); err == nil {
			parts[i] = "{id}"
		}
	}

	return strings.Join(parts, "/")
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./proxy/... -run TestNormalizePath -v
```

Expected: all cases `PASS`

- [ ] **Step 5: Commit**

```bash
git add proxy/pathutil.go proxy/pathutil_test.go
git commit -m "feat: add NormalizePath for beacon API path normalization"
```

---

## Task 3: Billing Metrics Counter

**Files:**
- Modify: `metrics/metrics.go`
- Create: `metrics/metrics_test.go`
- Modify: `cmd/dugtrio-proxy/main.go` (update `NewProxyMetrics` call)

- [ ] **Step 1: Write the failing test**

Create `metrics/metrics_test.go`:

```go
package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddBillingCall_IncrementsCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newProxyMetrics(nil, reg)

	m.AddBillingCall("ACM-ETH-01", "/eth/v1/node/version", "GET", "success")
	m.AddBillingCall("ACM-ETH-01", "/eth/v1/node/version", "GET", "success")
	m.AddBillingCall("GLB-ETH-01", "/eth/v1/beacon/blocks/{id}", "GET", "upstream_error")

	count, err := testutil.GatherAndCount(reg, "dugtrio_billing_requests_total")
	require.NoError(t, err)
	/* two distinct label sets registered */
	assert.Equal(t, 2, count)

	val := testutil.ToFloat64(m.billingRequests.With(prometheus.Labels{
		"billing_code": "ACM-ETH-01",
		"path":         "/eth/v1/node/version",
		"method":       "GET",
		"status":       "success",
	}))
	assert.Equal(t, float64(2), val)
}
```

- [ ] **Step 2: Run test — expect compile error**

```bash
go test ./metrics/...
```

Expected: compile error — `newProxyMetrics` and `AddBillingCall` undefined, `billingRequests` unexported field inaccessible.

- [ ] **Step 3: Refactor `NewProxyMetrics` to accept a `prometheus.Registerer` and add billing counter**

Replace the contents of `metrics/metrics.go` with:

```go
package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

type ProxyMetrics struct {
	totalCalls      prometheus.Counter
	clientCalls     *prometheus.CounterVec
	pathCalls       *prometheus.CounterVec
	callDuration    *prometheus.HistogramVec
	callStatus      *prometheus.CounterVec
	billingRequests *prometheus.CounterVec
}

// NewProxyMetrics registers all metrics against prometheus.DefaultRegisterer.
func NewProxyMetrics(beaconPool *pool.BeaconPool) *ProxyMetrics {
	return newProxyMetrics(beaconPool, prometheus.DefaultRegisterer)
}

// newProxyMetrics registers all metrics against the supplied registerer.
// Passing a fresh prometheus.NewRegistry() in tests prevents cross-test pollution.
func newProxyMetrics(beaconPool *pool.BeaconPool, reg prometheus.Registerer) *ProxyMetrics {
	factory := promauto.With(reg)

	m := &ProxyMetrics{
		totalCalls: factory.NewCounter(prometheus.CounterOpts{
			Name: "dugtrio_calls_total",
			Help: "The total number of proxy requests",
		}),
		clientCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_client_calls_total",
				Help: "Number of proxy requests per client.",
			},
			[]string{"client"},
		),
		pathCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_path_calls_total",
				Help: "Number of proxy requests per api path.",
			},
			[]string{"path"},
		),
		callDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "dugtrio_call_time",
				Help: "Duration of proxy requests.",
			},
			[]string{"client", "path"},
		),
		callStatus: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_call_status_total",
				Help: "Number of requests per pool client.",
			},
			[]string{"client", "path", "status"},
		),
		billingRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_billing_requests_total",
				Help: "Number of proxy requests per billing code, API path, HTTP method, and outcome status.",
			},
			[]string{"billing_code", "path", "method", "status"},
		),
	}

	for _, c := range []prometheus.Collector{
		m.clientCalls,
		m.pathCalls,
		m.callStatus,
		m.billingRequests,
	} {
		if err := reg.Register(c); err != nil {
			logrus.Errorf("error registering metric: %v", err)
		}
	}

	if beaconPool != nil {
		if err := reg.Register(prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Name: "dugtrio_pool_online",
				Help: "Number of online clients in the node pool.",
			},
			func() float64 {
				canonicalFork := beaconPool.GetCanonicalFork()
				if canonicalFork == nil {
					return 0
				}

				return float64(len(canonicalFork.ReadyClients))
			},
		)); err != nil {
			logrus.Errorf("error registering pool online metric: %v", err)
		}
	}

	return m
}

func (m *ProxyMetrics) AddCall(clientName, apiPath string, callDuration time.Duration, callStatus int) {
	trimmedPath := m.trimAPIPath(apiPath)

	m.totalCalls.Inc()
	m.clientCalls.With(prometheus.Labels{
		"client": clientName,
	}).Inc()
	m.pathCalls.With(prometheus.Labels{
		"path": trimmedPath,
	}).Inc()
	m.callDuration.With(prometheus.Labels{
		"client": clientName,
		"path":   trimmedPath,
	}).Observe(float64(callDuration.Milliseconds()) / 1000)
	m.callStatus.With(prometheus.Labels{
		"client": clientName,
		"path":   trimmedPath,
		"status": fmt.Sprintf("%v", callStatus),
	}).Inc()
}

// AddBillingCall records a completed proxy request attributed to a downstream
// billing code. path should already be normalized by NormalizePath.
// status is one of: "success", "upstream_error", "blocked", "no_upstream".
func (m *ProxyMetrics) AddBillingCall(billingCode, path, method, status string) {
	m.billingRequests.With(prometheus.Labels{
		"billing_code": billingCode,
		"path":         path,
		"method":       method,
		"status":       status,
	}).Inc()
}

func (m *ProxyMetrics) trimAPIPath(apiPath string) string {
	if queryPos := strings.Index(apiPath, "?"); queryPos > -1 {
		apiPath = apiPath[:queryPos]
	}

	pathParts := strings.Split(apiPath, "/")

	for i, pathPart := range pathParts {
		if i < 2 {
			continue
		}

		if strings.HasPrefix(pathPart, "0x") {
			pathParts[i] = "{hex}"
			continue
		}

		_, err := strconv.ParseUint(pathPart, 10, 64)
		if err == nil {
			pathParts[i] = "{id}"
			continue
		}
	}

	return strings.Join(pathParts, "/")
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./metrics/...
```

Expected: `PASS`

- [ ] **Step 5: Verify build still compiles**

```bash
go build ./...
```

Expected: no errors (`NewProxyMetrics` signature unchanged, the only public API change is `newProxyMetrics` which is unexported).

- [ ] **Step 6: Commit**

```bash
git add metrics/metrics.go metrics/metrics_test.go
git commit -m "feat: add billing requests metric counter to ProxyMetrics"
```

---

## Task 4: TokenRouter

**Files:**
- Create: `proxy/tokenrouter.go`
- Create: `proxy/tokenrouter_test.go`

- [ ] **Step 1: Write the failing tests**

Create `proxy/tokenrouter_test.go`:

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethpandaops/dugtrio/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClients() []*types.ClientConfig {
	return []*types.ClientConfig{
		{Token: "mytoken123", Name: "Acme Corp", BillingCode: "ACM-ETH-01"},
	}
}

func TestTokenRouter_InvalidToken_Returns401(t *testing.T) {
	called := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	tr := NewTokenRouter(backend, testClients())

	req := httptest.NewRequest("GET", "/wrongtoken/eth/v1/node/version", nil)
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, called, "backend must not be called for invalid token")
}

func TestTokenRouter_MissingToken_Returns401(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tr := NewTokenRouter(backend, testClients())

	req := httptest.NewRequest("GET", "/eth/v1/node/version", nil)
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenRouter_ValidToken_StripsPrefix(t *testing.T) {
	var capturedPath string

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	tr := NewTokenRouter(backend, testClients())

	req := httptest.NewRequest("GET", "/mytoken123/eth/v1/node/version", nil)
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/eth/v1/node/version", capturedPath)
}

func TestTokenRouter_ValidToken_SetsBillingCodeOnContext(t *testing.T) {
	var capturedCode string

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCode = getBillingCode(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	tr := NewTokenRouter(backend, testClients())

	req := httptest.NewRequest("GET", "/mytoken123/eth/v1/node/version", nil)
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	assert.Equal(t, "ACM-ETH-01", capturedCode)
}

func TestTokenRouter_FallbackCalledForUnknownToken(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	fallbackCalled := false
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalled = true
		w.WriteHeader(http.StatusOK)
	})

	tr := NewTokenRouter(backend, testClients()).WithFallback(fallback)

	req := httptest.NewRequest("GET", "/static/css/bootstrap.min.css", nil)
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	assert.True(t, fallbackCalled)
	assert.Equal(t, http.StatusOK, rec.Code)
}
```

- [ ] **Step 2: Run test — expect compile error**

```bash
go test ./proxy/... -run TestTokenRouter
```

Expected: compile error — `NewTokenRouter`, `getBillingCode` undefined.

- [ ] **Step 3: Implement `proxy/tokenrouter.go`**

Create `proxy/tokenrouter.go`:

```go
package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/ethpandaops/dugtrio/types"
)

type billingContextKey struct{}

// getBillingCode reads the billing code stored by TokenRouter from ctx.
// Returns empty string if the request did not come through a TokenRouter.
func getBillingCode(ctx context.Context) string {
	v, _ := ctx.Value(billingContextKey{}).(string)
	return v
}

// TokenRouter validates per-customer tokens embedded in the URL path prefix,
// strips the token before forwarding, and stores the billing code on the
// request context. Unknown tokens return 401 (or fall through to fallback).
type TokenRouter struct {
	handler  http.Handler
	fallback http.Handler
	tokens   map[string]*types.ClientConfig
}

// NewTokenRouter builds a TokenRouter from a slice of client configs.
// handler is the downstream proxy (BeaconProxy) that receives stripped requests.
func NewTokenRouter(handler http.Handler, clients []*types.ClientConfig) *TokenRouter {
	tokens := make(map[string]*types.ClientConfig, len(clients))
	for _, c := range clients {
		tokens[c.Token] = c
	}

	return &TokenRouter{handler: handler, tokens: tokens}
}

// WithFallback sets an optional handler called when the first path segment
// is not a registered token. Use this to pass static-file requests through
// to the frontend handler when both features are active.
func (tr *TokenRouter) WithFallback(h http.Handler) *TokenRouter {
	tr.fallback = h
	return tr
}

func (tr *TokenRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	sep := strings.IndexByte(path, '/')

	var token, rest string
	if sep < 0 {
		token = path
		rest = "/"
	} else {
		token = path[:sep]
		rest = path[sep:]
	}

	cfg, ok := tr.tokens[token]
	if !ok {
		if tr.fallback != nil {
			tr.fallback.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)

		if _, err := w.Write([]byte("Unauthorized")); err != nil {
			/* best-effort write, ignore error */
		}

		return
	}

	/* Clone request: strip token prefix and attach billing code to context.
	   The raw token is not stored or forwarded. */
	r2 := r.Clone(context.WithValue(r.Context(), billingContextKey{}, cfg.BillingCode))
	r2.URL = r.URL.ReqURI() // re-use scheme/host
	r2.URL.Path = rest

	if r.URL.RawPath != "" {
		rawRest := strings.TrimPrefix(r.URL.RawPath, "/"+token)
		if rawRest == "" {
			rawRest = "/"
		}

		r2.URL.RawPath = rawRest
	}

	tr.handler.ServeHTTP(w, r2)
}
```

> **Note on `r.URL.ReqURI()`**: gorilla/mux passes requests with the full URL populated. The safest clone is `r2.URL = new(url.URL); *r2.URL = *r.URL` followed by updating Path/RawPath. Use that pattern if `r.URL.ReqURI()` compiles incorrectly — see correction below:

Replace the URL clone lines with:

```go
	newURL := *r.URL
	newURL.Path = rest
	if r.URL.RawPath != "" {
		rawRest := strings.TrimPrefix(r.URL.RawPath, "/"+token)
		if rawRest == "" {
			rawRest = "/"
		}
		newURL.RawPath = rawRest
	}
	r2 := r.Clone(context.WithValue(r.Context(), billingContextKey{}, cfg.BillingCode))
	r2.URL = &newURL
```

Remove the earlier `r2.URL = r.URL.ReqURI()` line. The complete corrected `ServeHTTP`:

```go
func (tr *TokenRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	sep := strings.IndexByte(path, '/')

	var token, rest string
	if sep < 0 {
		token = path
		rest = "/"
	} else {
		token = path[:sep]
		rest = path[sep:]
	}

	cfg, ok := tr.tokens[token]
	if !ok {
		if tr.fallback != nil {
			tr.fallback.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)

		if _, err := w.Write([]byte("Unauthorized")); err != nil {
			/* best-effort write */
		}

		return
	}

	newURL := *r.URL
	newURL.Path = rest

	if r.URL.RawPath != "" {
		rawRest := strings.TrimPrefix(r.URL.RawPath, "/"+token)
		if rawRest == "" {
			rawRest = "/"
		}

		newURL.RawPath = rawRest
	}

	r2 := r.Clone(context.WithValue(r.Context(), billingContextKey{}, cfg.BillingCode))
	r2.URL = &newURL

	tr.handler.ServeHTTP(w, r2)
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./proxy/... -run TestTokenRouter -v
```

Expected: all 5 `TestTokenRouter_*` tests pass.

- [ ] **Step 5: Commit**

```bash
git add proxy/tokenrouter.go proxy/tokenrouter_test.go
git commit -m "feat: add TokenRouter with per-customer billing code context"
```

---

## Task 5: Wire Billing Metric into processCall

**Files:**
- Modify: `proxy/beaconproxy.go`

The goal: defer a call to `AddBillingCall` at the start of `processCall` with a `callStatus` variable that is updated at each exit point. Only fires when a `billing_code` is present on the context (i.e., request came through `TokenRouter`).

- [ ] **Step 1: Add billing defer to `processCall` in `proxy/beaconproxy.go`**

In `processCall`, directly after `rw := &responseWriterTracker{ResponseWriter: w}`, insert:

```go
	billingCode := getBillingCode(r.Context())
	callStatus := "no_upstream"

	if billingCode != "" && proxy.proxyMetrics != nil {
		defer func() {
			proxy.proxyMetrics.AddBillingCall(
				billingCode,
				NormalizePath(r.URL.EscapedPath()),
				r.Method,
				callStatus,
			)
		}()
	}
```

- [ ] **Step 2: Set `callStatus = "blocked"` on the blocked-path return**

Find the `checkBlockedPaths` block:

```go
	if proxy.checkBlockedPaths(r.URL) {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusForbidden)
		...
		return
	}
```

Insert `callStatus = "blocked"` before the `return`:

```go
	if proxy.checkBlockedPaths(r.URL) {
		callStatus = "blocked"
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusForbidden)

		if _, err := rw.Write([]byte("Path Blocked")); err != nil {
			proxy.logger.Warnf("error writing path blocked response: %v", err)
		}

		return
	}
```

- [ ] **Step 3: Set `callStatus = "upstream_error"` when all endpoints have been tried**

In the retry loop, find:

```go
		endpoint := proxy.pool.GetReadyEndpointExcluding(clientType, tried)
		if endpoint == nil {
			rw.Header().Set("Content-Type", "text/plain")
			rw.WriteHeader(http.StatusServiceUnavailable)
			...
			return
		}
```

Insert `callStatus` update before the return:

```go
		endpoint := proxy.pool.GetReadyEndpointExcluding(clientType, tried)
		if endpoint == nil {
			if len(tried) > 0 {
				callStatus = "upstream_error"
			}

			rw.Header().Set("Content-Type", "text/plain")
			rw.WriteHeader(http.StatusServiceUnavailable)

			if _, err := rw.Write([]byte("no upstream available")); err != nil {
				proxy.logger.Warnf("error writing no upstream response: %v", err)
			}

			return
		}
```

- [ ] **Step 4: Set `callStatus = "success"` before `writeProxyResponse`**

Find the call to `writeProxyResponse` in the retry loop. Insert `callStatus = "success"` directly before it:

```go
		session.requests.Add(1)
		callStatus = "success"

		if _, err = proxy.writeProxyResponse(rw, r, session, resp, endpoint, callCtx); err != nil {
			proxy.logger.WithFields(logrus.Fields{
				"endpoint": endpoint.GetName(),
			}).Warnf("proxy stream error: %v", err)
		}

		attemptCancel()

		return
```

- [ ] **Step 5: Build to confirm no compile errors**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Run existing proxy tests**

```bash
go test ./...
```

Expected: all tests pass (no existing proxy tests touch processCall directly, so no regressions expected).

- [ ] **Step 7: Commit**

```bash
git add proxy/beaconproxy.go
git commit -m "feat: track billing requests metric per processCall outcome"
```

---

## Task 6: Route Registration and require_tokens Guard

**Files:**
- Modify: `cmd/dugtrio-proxy/main.go`

- [ ] **Step 1: Add `requireTokensGuard` helper and update route registration**

Open `cmd/dugtrio-proxy/main.go`. After the `startDugtrio` function signature, add a private helper (place it below `startHTTPServer`):

```go
// requireTokensGuard wraps h to return 401 when require_tokens is enabled,
// blocking legacy direct-access routes during migration Phase 3.
func requireTokensGuard(required bool, h http.Handler) http.Handler {
	if !required {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)

		if _, err := w.Write([]byte("Unauthorized")); err != nil {
			logrus.Warnf("error writing unauthorized response: %v", err)
		}
	})
}
```

- [ ] **Step 2: Wrap all legacy proxy routes with the guard**

In `startDugtrio`, replace the legacy route registrations with guarded versions. Change:

```go
	// standardized beacon node endpoints
	router.PathPrefix("/eth/").Handler(beaconProxy)

	// client specific endpoints
	router.PathPrefix("/caplin/").Handler(beaconProxy.NewClientSpecificProxy(pool.CaplinClient))
	router.PathPrefix("/grandine/").Handler(beaconProxy.NewClientSpecificProxy(pool.GrandineClient))
	router.PathPrefix("/lighthouse/").Handler(beaconProxy.NewClientSpecificProxy(pool.LighthouseClient))
	router.PathPrefix("/lodestar/").Handler(beaconProxy.NewClientSpecificProxy(pool.LodestarClient))
	router.PathPrefix("/nimbus/").Handler(beaconProxy.NewClientSpecificProxy(pool.NimbusClient))
	router.PathPrefix("/prysm/").Handler(beaconProxy.NewClientSpecificProxy(pool.PrysmClient))
	router.PathPrefix("/teku/").Handler(beaconProxy.NewClientSpecificProxy(pool.TekuClient))
```

To:

```go
	requireTokens := config.Proxy != nil && config.Proxy.RequireTokens

	// standardized beacon node endpoints
	router.PathPrefix("/eth/").Handler(requireTokensGuard(requireTokens, beaconProxy))

	// client specific endpoints
	router.PathPrefix("/caplin/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.CaplinClient)))
	router.PathPrefix("/grandine/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.GrandineClient)))
	router.PathPrefix("/lighthouse/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.LighthouseClient)))
	router.PathPrefix("/lodestar/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.LodestarClient)))
	router.PathPrefix("/nimbus/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.NimbusClient)))
	router.PathPrefix("/prysm/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.PrysmClient)))
	router.PathPrefix("/teku/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.TekuClient)))
```

- [ ] **Step 3: Register the token router catch-all**

In `startDugtrio`, just before the `if config.Frontend.Enabled` block, add:

```go
	// Token router — registered after specific prefixes so they take priority.
	// When frontend is also enabled, the token router falls back to the static
	// file handler so that /static/, /webfonts/, favicon etc. still resolve.
	if config.Proxy != nil && len(config.Proxy.Clients) > 0 {
		tokenRouter := proxy.NewTokenRouter(beaconProxy, config.Proxy.Clients)

		if config.Frontend.Enabled {
			frontendBaseHandler, err := frontend.NewFrontend(config.Frontend)
			if err != nil {
				logrus.Fatalf("error initializing frontend: %v", err)
			}

			tokenRouter = tokenRouter.WithFallback(frontendBaseHandler)
			router.PathPrefix("/").Handler(tokenRouter)

			// Register named frontend pages before the catch-all just built above
			// would have caught them — insert them into the mux AFTER the catch-all
			// via HandleFunc (gorilla/mux exact routes win over PathPrefix).
			frontendHandler := handlers.NewFrontendHandler(beaconPool, beaconProxy)
			router.HandleFunc("/", frontendHandler.Index).Methods("GET")
			router.HandleFunc("/health", frontendHandler.Health).Methods("GET")
			router.HandleFunc("/sessions", frontendHandler.Sessions).Methods("GET")
		} else {
			router.PathPrefix("/").Handler(tokenRouter)
		}
	}
```

> **Important:** Because gorilla/mux `HandleFunc` with an exact path is scored as more specific than `PathPrefix("/")`, the frontend page routes registered after the catch-all still match first for exact paths (`/`, `/health`, `/sessions`). Only unmatched paths (including `/{token}/...` and static files) fall through to `TokenRouter`.

- [ ] **Step 4: Remove the duplicate frontend block**

The `if config.Frontend.Enabled` block that originally registered the frontend must be removed (or guarded to only run when token routing is inactive) to avoid double-registration of `/` and the catch-all.

Update the `if config.Frontend.Enabled` block so it only runs when there are no clients configured:

```go
	if config.Frontend.Enabled && (config.Proxy == nil || len(config.Proxy.Clients) == 0) {
		frontendBaseHandler, err := frontend.NewFrontend(config.Frontend)
		if err != nil {
			logrus.Fatalf("error initializing frontend: %v", err)
		}

		frontendHandler := handlers.NewFrontendHandler(beaconPool, beaconProxy)
		router.HandleFunc("/", frontendHandler.Index).Methods("GET")
		router.HandleFunc("/health", frontendHandler.Health).Methods("GET")
		router.HandleFunc("/sessions", frontendHandler.Sessions).Methods("GET")
		router.PathPrefix("/").Handler(frontendBaseHandler)
	}
```

- [ ] **Step 5: Build**

```bash
go build ./...
```

Expected: no errors. Fix any import issues (`proxy` package import may need to be added to main.go imports).

- [ ] **Step 6: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 7: Smoke test binary starts**

```bash
cat > /tmp/test-dugtrio.yaml << 'EOF'
endpoints:
  - name: "test"
    url: "http://localhost:5052"
proxy:
  require_tokens: false
  clients:
    - token: "testtoken123"
      name: "Test Client"
      billing_code: "TST-ETH-01"
metrics:
  enabled: true
EOF

go run ./cmd/dugtrio-proxy --config /tmp/test-dugtrio.yaml &
sleep 1
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/wrongtoken/eth/v1/node/version
# Expected: 401
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/eth/v1/node/version
# Expected: 503 (no upstream connected, but proxy is up)
kill %1
```

- [ ] **Step 8: Commit**

```bash
git add cmd/dugtrio-proxy/main.go
git commit -m "feat: register TokenRouter and require_tokens guard in main"
```

---

## Self-Review Notes

- **Spec coverage:** Config ✓ · TokenRouter ✓ · path normalization ✓ · billing metrics ✓ · require_tokens ✓ · rollout phases documented in spec (no code needed) · token masking (never stored/forwarded) ✓
- **Type consistency:** `BillingCode` field name used consistently across config, tokenrouter, beaconproxy. `getBillingCode` / `billingContextKey` defined once in `tokenrouter.go` and used in `beaconproxy.go` (same package).
- **No placeholders:** All steps contain complete code.
- **YAGNI:** No per-token rate limit, no dynamic API, no subdomain routing — all deferred per spec.
