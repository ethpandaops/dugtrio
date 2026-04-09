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

// WithFallback sets an optional handler called when the first path segment is
// not a registered token. Use this to pass static-file requests through to the
// frontend handler when both features are active.
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
			/* best-effort write */
		}

		return
	}

	/* Clone request: strip token prefix and attach billing code to context.
	   The raw token is not stored or forwarded. */
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
