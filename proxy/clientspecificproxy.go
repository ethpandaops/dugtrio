package proxy

import (
	"net/http"
	"strings"

	"github.com/ethpandaops/dugtrio/pool"
)

type ClientSpecificProxy struct {
	beaconProxy *BeaconProxy
	clientType  pool.ClientType
	pathPrefix  string
}

func (proxy *BeaconProxy) NewClientSpecificProxy(clientType pool.ClientType) *ClientSpecificProxy {
	pathPrefix := clientType.String()
	if pathPrefix != "" {
		pathPrefix = "/" + strings.ToLower(pathPrefix) + "/"
	}

	return &ClientSpecificProxy{
		beaconProxy: proxy,
		clientType:  clientType,
		pathPrefix:  pathPrefix,
	}
}

func (proxy *ClientSpecificProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip the client-specific prefix only for standard beacon API paths (/eth/v*)
	// This allows both standard routes like /lodestar/eth/v1/node/version
	// and client-specific paths like /lighthouse/syncing to work
	if proxy.pathPrefix != "" && strings.HasPrefix(r.URL.Path, proxy.pathPrefix) {
		remainingPath := strings.TrimPrefix(r.URL.Path, proxy.pathPrefix)
		// Only strip the prefix if the remaining path is a standard beacon API path
		if strings.HasPrefix(remainingPath, "eth/v") {
			r.URL.Path = "/" + remainingPath
		}
		// Otherwise keep the full path intact for client-specific endpoints
	}

	proxy.beaconProxy.processCall(w, r, proxy.clientType)
}
