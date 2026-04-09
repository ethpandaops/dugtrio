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
