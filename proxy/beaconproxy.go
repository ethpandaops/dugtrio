package proxy

import (
	"net/http"

	"github.com/ethpandaops/dugtrio/pool"
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
}
