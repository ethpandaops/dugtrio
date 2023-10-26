package handlers

import "github.com/ethpandaops/dugtrio/pool"

type FrontendHandler struct {
	pool *pool.BeaconPool
}

func NewFrontendHandler(beaconPool *pool.BeaconPool) *FrontendHandler {
	return &FrontendHandler{
		pool: beaconPool,
	}
}
