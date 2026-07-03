package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/types"
)

const testFailoverPath = "/eth/v1/beacon/states/123/validators"

type testEndpoint struct {
	client *pool.Client
	hits   atomic.Int32
}

// newTestEndpoint spins up a mock upstream and registers it in the pool. Only calls to the
// failover test path reach the given handler; everything else (the pool client's health
// checks) gets an instant 404 so the background pool loop fails fast and stays quiet.
func newTestEndpoint(t *testing.T, beaconPool *pool.BeaconPool, name string, handler http.HandlerFunc) *testEndpoint {
	t.Helper()

	endpoint := &testEndpoint{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/eth/v1/beacon/states/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		endpoint.hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	client, err := beaconPool.AddEndpoint(&types.EndpointConfig{Name: name, URL: server.URL})
	if err != nil {
		t.Fatalf("error adding test endpoint %v: %v", name, err)
	}

	endpoint.client = client

	return endpoint
}

func newTestProxy(t *testing.T, config *types.ProxyConfig) (*BeaconProxy, *pool.BeaconPool) {
	t.Helper()

	beaconPool, err := pool.NewBeaconPool(&types.PoolConfig{FollowDistance: 10})
	if err != nil {
		t.Fatalf("error creating test pool: %v", err)
	}

	proxy, err := NewBeaconProxy(config, beaconPool, nil)
	if err != nil {
		t.Fatalf("error creating test proxy: %v", err)
	}

	return proxy, beaconPool
}

func runTestProxyAttempts(t *testing.T, proxy *BeaconProxy, first *testEndpoint, failoverCtx *failoverContext) (*http.Response, *pool.Client, error) {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, testFailoverPath, http.NoBody)

	callContext := proxy.newProxyCallContext(context.Background(), proxy.config.CallTimeout)
	t.Cleanup(callContext.cancelFn)

	return proxy.runProxyAttempts(r, callContext, first.client, failoverCtx)
}

func statusHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// hangingHandler blocks until the request is cancelled by the proxy (or the test ends).
func hangingHandler(t *testing.T) http.HandlerFunc {
	t.Helper()

	done := make(chan struct{})

	t.Cleanup(func() { close(done) })

	return func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}
}

func readTestResponse(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	return string(body)
}

// fast upstream failures advance through the candidates until one can serve the data
func TestFailoverSweepsFastFailures(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 5 * time.Second, FailoverHedgeDelay: time.Second})

	endpointA := newTestEndpoint(t, beaconPool, "a", statusHandler(http.StatusNotFound, "a-err"))
	endpointB := newTestEndpoint(t, beaconPool, "b", statusHandler(http.StatusInternalServerError, "b-err"))
	endpointC := newTestEndpoint(t, beaconPool, "c", statusHandler(http.StatusOK, "c-ok"))

	failoverCtx := &failoverContext{candidates: []*pool.Client{endpointB.client, endpointC.client}, maxAttempts: 8}

	resp, endpoint, err := runTestProxyAttempts(t, proxy, endpointA, failoverCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if endpoint != endpointC.client || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from endpoint c, got %v from %v", resp.StatusCode, endpoint.GetName())
	}

	if body := readTestResponse(t, resp); body != "c-ok" {
		t.Fatalf("unexpected response body: %v", body)
	}

	if failoverCtx.attempts != 2 {
		t.Fatalf("expected 2 failover attempts, got %v", failoverCtx.attempts)
	}
}

// a silent endpoint gets hedged: the next candidate is raced after failoverHedgeDelay
func TestFailoverHedgesSilentEndpoint(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 5 * time.Second, FailoverHedgeDelay: 100 * time.Millisecond})

	endpointA := newTestEndpoint(t, beaconPool, "a", hangingHandler(t))
	endpointB := newTestEndpoint(t, beaconPool, "b", statusHandler(http.StatusOK, "b-ok"))

	failoverCtx := &failoverContext{candidates: []*pool.Client{endpointB.client}, maxAttempts: 8}

	start := time.Now()

	resp, endpoint, err := runTestProxyAttempts(t, proxy, endpointA, failoverCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if endpoint != endpointB.client || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from endpoint b, got %v from %v", resp.StatusCode, endpoint.GetName())
	}

	resp.Body.Close()

	if elapsed := time.Since(start); elapsed < 100*time.Millisecond || elapsed > time.Second {
		t.Fatalf("expected hedged response after ~100ms, took %v", elapsed)
	}
}

// a slow endpoint is not cancelled by the hedge delay: it keeps running and can still win
// even after hedged candidates were started (regression test for the cascade problem where
// no endpoint got enough time to load a state)
func TestFailoverSlowEndpointStillWins(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 5 * time.Second, FailoverHedgeDelay: 100 * time.Millisecond})

	endpointA := newTestEndpoint(t, beaconPool, "a", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("a-slow-ok"))
	})
	endpointB := newTestEndpoint(t, beaconPool, "b", hangingHandler(t))
	endpointC := newTestEndpoint(t, beaconPool, "c", hangingHandler(t))

	failoverCtx := &failoverContext{candidates: []*pool.Client{endpointB.client, endpointC.client}, maxAttempts: 8}

	resp, endpoint, err := runTestProxyAttempts(t, proxy, endpointA, failoverCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if endpoint != endpointA.client {
		t.Fatalf("expected slow endpoint a to win, got response from %v", endpoint.GetName())
	}

	if body := readTestResponse(t, resp); body != "a-slow-ok" {
		t.Fatalf("unexpected response body: %v", body)
	}
}

// when no endpoint can serve the data, the last upstream failure is passed through
func TestFailoverFallbackResponse(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 5 * time.Second, FailoverHedgeDelay: time.Second})

	endpointA := newTestEndpoint(t, beaconPool, "a", statusHandler(http.StatusNotFound, "a-err"))
	endpointB := newTestEndpoint(t, beaconPool, "b", statusHandler(http.StatusNotFound, "b-err"))
	endpointC := newTestEndpoint(t, beaconPool, "c", statusHandler(http.StatusNotFound, "c-err"))

	failoverCtx := &failoverContext{candidates: []*pool.Client{endpointB.client, endpointC.client}, maxAttempts: 8}

	resp, endpoint, err := runTestProxyAttempts(t, proxy, endpointA, failoverCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if endpoint != endpointC.client || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected fallback 404 from endpoint c, got %v from %v", resp.StatusCode, endpoint.GetName())
	}

	if body := readTestResponse(t, resp); body != "c-err" {
		t.Fatalf("unexpected response body: %v", body)
	}
}

// hedging never runs more than failoverMaxParallel attempts at once
func TestFailoverMaxParallel(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 700 * time.Millisecond, FailoverHedgeDelay: 100 * time.Millisecond, FailoverMaxParallel: 2})

	endpointA := newTestEndpoint(t, beaconPool, "a", hangingHandler(t))
	endpointB := newTestEndpoint(t, beaconPool, "b", hangingHandler(t))
	endpointC := newTestEndpoint(t, beaconPool, "c", hangingHandler(t))
	endpointD := newTestEndpoint(t, beaconPool, "d", hangingHandler(t))

	failoverCtx := &failoverContext{candidates: []*pool.Client{endpointB.client, endpointC.client, endpointD.client}, maxAttempts: 8}

	_, _, err := runTestProxyAttempts(t, proxy, endpointA, failoverCtx) //nolint:bodyclose // the response is nil, all attempts fail
	if err == nil {
		t.Fatalf("expected error when all endpoints hang")
	}

	if hits := endpointA.hits.Load(); hits != 1 {
		t.Fatalf("expected 1 attempt on endpoint a, got %v", hits)
	}

	if hits := endpointB.hits.Load(); hits != 1 {
		t.Fatalf("expected 1 hedged attempt on endpoint b, got %v", hits)
	}

	if hits := endpointC.hits.Load() + endpointD.hits.Load(); hits != 0 {
		t.Fatalf("expected no attempts beyond failoverMaxParallel, got %v", hits)
	}
}

// buffered POST bodies are replayed on every attempt
func TestFailoverReplaysRequestBody(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 5 * time.Second, FailoverHedgeDelay: time.Second})

	var receivedBody atomic.Pointer[string]

	endpointA := newTestEndpoint(t, beaconPool, "a", statusHandler(http.StatusNotFound, "a-err"))
	endpointB := newTestEndpoint(t, beaconPool, "b", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)
		receivedBody.Store(&bodyStr)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("b-ok"))
	})

	reqBody := `["1","2"]`
	failoverCtx := &failoverContext{
		body:        []byte(reqBody),
		hasBody:     true,
		candidates:  []*pool.Client{endpointB.client},
		maxAttempts: 8,
	}

	r := httptest.NewRequest(http.MethodPost, testFailoverPath, bytes.NewReader([]byte(reqBody)))

	callContext := proxy.newProxyCallContext(context.Background(), proxy.config.CallTimeout)
	t.Cleanup(callContext.cancelFn)

	resp, endpoint, err := proxy.runProxyAttempts(r, callContext, endpointA.client, failoverCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if endpoint != endpointB.client || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from endpoint b, got %v from %v", resp.StatusCode, endpoint.GetName())
	}

	resp.Body.Close()

	if got := receivedBody.Load(); got == nil || *got != reqBody {
		t.Fatalf("expected replayed request body %q, got %v", reqBody, got)
	}
}

// calls without a failover context still work as plain single-attempt proxy calls
func TestProxyAttemptWithoutFailover(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{CallTimeout: 5 * time.Second})

	endpointA := newTestEndpoint(t, beaconPool, "a", statusHandler(http.StatusNotFound, "a-err"))

	resp, endpoint, err := runTestProxyAttempts(t, proxy, endpointA, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if endpoint != endpointA.client || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected passthrough 404 from endpoint a, got %v from %v", resp.StatusCode, endpoint.GetName())
	}

	if body := readTestResponse(t, resp); body != "a-err" {
		t.Fatalf("unexpected response body: %v", body)
	}
}
