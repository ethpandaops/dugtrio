package proxy

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethpandaops/dugtrio/types"
)

// TestCheckAuthorizationBasicAuthWithNilConfig sends a Basic-auth header at a proxy
// that has no auth block configured at all. Auth is nil in that case, and the
// basic-auth branch used to dereference it unconditionally, panicking on every such
// request regardless of who sent it.
func TestCheckAuthorizationBasicAuthWithNilConfig(t *testing.T) {
	proxy := &BeaconProxy{config: &types.ProxyConfig{}}

	req := httptest.NewRequest(http.MethodGet, "/eth/v1/node/version", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))

	identifier, ok := proxy.CheckAuthorization(req)

	if identifier != "" {
		t.Fatalf("expected empty identifier, got %q", identifier)
	}

	if !ok {
		t.Fatal("expected the request to be allowed since auth is not required by default")
	}
}

// TestCheckAuthorizationBasicAuthWithConfiguredPassword confirms normal Basic-auth
// still works correctly when an auth block is configured, so the nil guard does not
// interfere with the real authentication path.
func TestCheckAuthorizationBasicAuthWithConfiguredPassword(t *testing.T) {
	proxy := &BeaconProxy{config: &types.ProxyConfig{
		Auth: &types.AuthConfig{
			Required: true,
			Password: "correct-password",
		},
	}}

	correctReq := httptest.NewRequest(http.MethodGet, "/eth/v1/node/version", nil)
	correctReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:correct-password")))

	identifier, ok := proxy.CheckAuthorization(correctReq)
	if identifier != "alice" || !ok {
		t.Fatalf("expected (alice, true), got (%q, %v)", identifier, ok)
	}

	wrongReq := httptest.NewRequest(http.MethodGet, "/eth/v1/node/version", nil)
	wrongReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:wrong-password")))

	identifier, ok = proxy.CheckAuthorization(wrongReq)
	if ok {
		t.Fatalf("expected the wrong password to be rejected, got (%q, %v)", identifier, ok)
	}
}
