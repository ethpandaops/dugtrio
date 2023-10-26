package proxy

import (
	"fmt"
	"net/http"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/utils"
)

type BeaconProxy struct {
	pool *pool.BeaconPool
}

func NewBeaconProxy(beaconPool *pool.BeaconPool) (*BeaconProxy, error) {
	proxy := BeaconProxy{
		pool: beaconPool,
	}
	return &proxy, nil
}

func (proxy *BeaconProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO: serve proxy call

	endpoint := proxy.pool.GetReadyEndpoint()
	endpointConfig := endpoint.GetEndpointConfig()

	w.Write([]byte(fmt.Sprintf("proxy call via %v (%v)", endpoint.GetName(), utils.GetRedactedUrl(endpointConfig.Url))))
}
