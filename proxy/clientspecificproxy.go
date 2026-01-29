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
	// Strip the client-specific prefix from the path before forwarding
	if proxy.pathPrefix != "" && strings.HasPrefix(r.URL.Path, proxy.pathPrefix) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, proxy.pathPrefix)
		// Ensure path starts with /
		if !strings.HasPrefix(r.URL.Path, "/") {
			r.URL.Path = "/" + r.URL.Path
		}
	}

	proxy.beaconProxy.processCall(w, r, proxy.clientType)
}
