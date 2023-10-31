package proxy

import (
	"net/http"

	"github.com/ethpandaops/dugtrio/pool"
)

type ClientSpecificProxy struct {
	beaconProxy *BeaconProxy
	clientType  pool.ClientType
}

func (proxy *BeaconProxy) NewClientSpecificProxy(clientType pool.ClientType) *ClientSpecificProxy {
	return &ClientSpecificProxy{
		beaconProxy: proxy,
		clientType:  clientType,
	}
}

func (proxy *ClientSpecificProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxy.beaconProxy.processCall(w, r, proxy.clientType)
}
