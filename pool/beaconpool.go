package pool

import (
	"fmt"
	"sync"

	"github.com/ethpandaops/dugtrio/types"
)

type SchedulerMode uint8

var (
	RoundRobinScheduler SchedulerMode = 1
)

type BeaconPool struct {
	config         *types.PoolConfig
	clientCounter  uint16
	clients        []*PoolClient
	blockCache     *BlockCache
	forkCacheMutex sync.Mutex
	forkCache      []*HeadFork

	schedulerMode  SchedulerMode
	schedulerMutex sync.Mutex
	rrLastIndexes  map[ClientType]uint16
}

func NewBeaconPool(config *types.PoolConfig) (*BeaconPool, error) {
	pool := BeaconPool{
		config:        config,
		clients:       make([]*PoolClient, 0),
		rrLastIndexes: map[ClientType]uint16{},
	}
	var err error

	switch config.SchedulerMode {
	case "", "rr", "roundrobin":
		pool.schedulerMode = RoundRobinScheduler
	default:
		return nil, fmt.Errorf("unknown pool schedulerMode: %v", config.SchedulerMode)
	}

	pool.blockCache, err = NewBlockCache(config.FollowDistance)
	if err != nil {
		return nil, err
	}
	return &pool, nil
}

func (pool *BeaconPool) GetBlockCache() *BlockCache {
	return pool.blockCache
}

func (pool *BeaconPool) AddEndpoint(endpoint *types.EndpointConfig) (*PoolClient, error) {
	clientIdx := pool.clientCounter
	pool.clientCounter++
	client, err := pool.newPoolClient(clientIdx, endpoint)
	if err != nil {
		return nil, err
	}
	pool.clients = append(pool.clients, client)
	return client, nil
}

func (pool *BeaconPool) GetAllEndpoints() []*PoolClient {
	return pool.clients
}

func (pool *BeaconPool) GetReadyEndpoint(clientType ClientType) *PoolClient {
	canonicalFork := pool.GetCanonicalFork()
	if canonicalFork == nil {
		return nil
	}

	readyClients := canonicalFork.ReadyClients
	if len(readyClients) == 0 {
		return nil
	}
	selectedClient := pool.runClientScheduler(readyClients, clientType)

	return selectedClient
}

func (pool *BeaconPool) IsClientReady(client *PoolClient) bool {
	if client == nil {
		return false
	}

	canonicalFork := pool.GetCanonicalFork()
	if canonicalFork == nil {
		return false
	}

	readyClients := canonicalFork.ReadyClients
	for _, readyClient := range readyClients {
		if readyClient == client {
			return true
		}
	}
	return false
}

func (pool *BeaconPool) runClientScheduler(readyClients []*PoolClient, clientType ClientType) *PoolClient {
	pool.schedulerMutex.Lock()
	defer pool.schedulerMutex.Unlock()

	switch pool.schedulerMode {
	case RoundRobinScheduler:
		var firstReadyClient *PoolClient
		for _, client := range readyClients {
			if clientType != UnspecifiedClient && clientType != client.clientType {
				continue
			}
			if firstReadyClient == nil {
				firstReadyClient = client
			}
			if client.clientIdx > pool.rrLastIndexes[clientType] {
				pool.rrLastIndexes[clientType] = client.clientIdx
				return client
			}
		}
		if firstReadyClient == nil {
			return nil
		} else {
			pool.rrLastIndexes[clientType] = firstReadyClient.clientIdx
			return firstReadyClient
		}
	}

	return readyClients[0]
}
