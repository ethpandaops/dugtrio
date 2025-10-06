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
	clients        []*Client
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
		clients:       make([]*Client, 0),
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

func (pool *BeaconPool) AddEndpoint(endpoint *types.EndpointConfig) (*Client, error) {
	clientIdx := pool.clientCounter
	pool.clientCounter++

	client, err := pool.newPoolClient(clientIdx, endpoint)
	if err != nil {
		return nil, err
	}

	pool.clients = append(pool.clients, client)

	return client, nil
}

func (pool *BeaconPool) GetAllEndpoints() []*Client {
	return pool.clients
}

func (pool *BeaconPool) GetEndpointByName(name string) *Client {
	for _, client := range pool.clients {
		if client.GetName() == name {
			return client
		}
	}

	return nil
}

func (pool *BeaconPool) GetReadyEndpoint(clientType ClientType, minCgc uint16) *Client {
	canonicalFork := pool.GetCanonicalFork()
	if canonicalFork == nil {
		return nil
	}

	readyClients := canonicalFork.ReadyClients
	if len(readyClients) == 0 {
		return nil
	}

	selectedClient := pool.runClientScheduler(readyClients, clientType, minCgc)

	return selectedClient
}

func (pool *BeaconPool) IsClientReady(client *Client) bool {
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

func (pool *BeaconPool) runClientScheduler(readyClients []*Client, clientType ClientType, minCgc uint16) *Client {
	pool.schedulerMutex.Lock()
	defer pool.schedulerMutex.Unlock()

	if minCgc > 0 {
		filteredClients := make([]*Client, 0, len(readyClients))
		for _, client := range readyClients {
			if client.GetCustodyGroupCount() >= minCgc {
				filteredClients = append(filteredClients, client)
			}
		}

		readyClients = filteredClients
	}

	if pool.schedulerMode == RoundRobinScheduler {
		var firstReadyClient *Client

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
		}

		pool.rrLastIndexes[clientType] = firstReadyClient.clientIdx

		return firstReadyClient
	}

	return readyClients[0]
}
