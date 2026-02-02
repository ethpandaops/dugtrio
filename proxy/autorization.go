package proxy

import (
	"encoding/base64"
	"net/http"
	"strings"
)

func (proxy *BeaconProxy) CheckAuthorization(r *http.Request) (string, bool) {
	requireAuth := proxy.config.Auth != nil && proxy.config.Auth.Required

	// Check for API key in X-Dugtrio-Secret-Token header first
	apiKey := r.Header.Get("X-Dugtrio-Secret-Token")
	if apiKey != "" && proxy.config.Auth != nil {
		for _, key := range proxy.config.Auth.ApiKeys {
			if key.Key == apiKey {
				return key.Name, true
			}
		}
		// API key provided but invalid
		return "", !requireAuth
	}

	// Fall back to Basic Auth
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", !requireAuth
	}

	// Check the auth type
	if !strings.HasPrefix(authHeader, "Basic ") {
		return "", !requireAuth
	}

	// decode the header
	decoded, err := base64.StdEncoding.DecodeString(authHeader[6:])
	if err != nil {
		return "", !requireAuth
	}

	// split the header into user and password
	creds := strings.Split(string(decoded), ":")
	if len(creds) != 2 {
		return "", !requireAuth
	}

	// check the password
	if proxy.config.Auth.Password == "" || creds[1] != proxy.config.Auth.Password {
		return "", !requireAuth
	}

	return creds[0], true
}
