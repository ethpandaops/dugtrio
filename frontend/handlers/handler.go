package handlers

import (
	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/proxy"
)

type FrontendHandler struct {
	pool  *pool.BeaconPool
	proxy *proxy.BeaconProxy
}

func NewFrontendHandler(beaconPool *pool.BeaconPool, beaconProxy *proxy.BeaconProxy) *FrontendHandler {
	return &FrontendHandler{
		pool:  beaconPool,
		proxy: beaconProxy,
	}
}
