package pool

import "github.com/ethpandaops/dugtrio/types"

type BeaconPool struct {
	clientCounter uint16
	clients       []*PoolClient
	blockCache    *BlockCache
}

func NewBeaconPool(config *types.PoolConfig) (*BeaconPool, error) {
	pool := BeaconPool{
		clients: make([]*PoolClient, 0),
	}
	var err error
	pool.blockCache, err = NewBlockCache(config.FollowDistance)
	if err != nil {
		return nil, err
	}
	return &pool, nil
}

func (pool *BeaconPool) AddEndpoint(endpoint *types.EndpointConfig) (*PoolClient, error) {
	clientIdx := pool.clientCounter
	pool.clientCounter++
	client, err := newUpstreamClient(pool.blockCache, clientIdx, endpoint)
	if err != nil {
		return nil, err
	}
	pool.clients = append(pool.clients, client)
	return client, nil
}

func (pool *BeaconPool) GetAllEndpoints() []*PoolClient {
	return pool.clients
}

func (pool *BeaconPool) GetReadyEndpoint() *PoolClient {
	// TODO: check for ready clients
	return pool.clients[0]
}
