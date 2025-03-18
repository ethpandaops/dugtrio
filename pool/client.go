package pool

import (
	"context"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dugtrio/rpc"
	"github.com/ethpandaops/dugtrio/types"
)

type PoolClient struct {
	beaconPool      *BeaconPool
	clientIdx       uint16
	endpointConfig  *types.EndpointConfig
	clientCtx       context.Context
	clientCtxCancel context.CancelFunc
	rpcClient       *rpc.BeaconClient
	logger          *logrus.Entry
	isOnline        bool
	isSyncing       bool
	isOptimistic    bool
	versionStr      string
	clientType      ClientType
	lastEvent       time.Time
	lastSyncCheck   time.Time
	retryCounter    uint64
	lastError       error
	headMutex       sync.RWMutex
	headRoot        phase0.Root
	headSlot        phase0.Slot
	finalizedRoot   phase0.Root
	finalizedEpoch  phase0.Epoch
}

func (pool *BeaconPool) newPoolClient(clientIdx uint16, endpoint *types.EndpointConfig) (*PoolClient, error) {
	rpcClient, err := rpc.NewBeaconClient(endpoint)
	if err != nil {
		return nil, err
	}

	client := PoolClient{
		beaconPool:     pool,
		clientIdx:      clientIdx,
		endpointConfig: endpoint,
		rpcClient:      rpcClient,
		logger:         logrus.WithField("client", endpoint.Name),
	}
	client.resetContext()
	go client.runPoolClientLoop()
	return &client, nil
}

func (client *PoolClient) resetContext() {
	if client.clientCtxCancel != nil {
		client.clientCtxCancel()
	}
	client.clientCtx, client.clientCtxCancel = context.WithCancel(context.Background())
}

func (client *PoolClient) GetIndex() uint16 {
	return client.clientIdx
}

func (client *PoolClient) GetName() string {
	return client.endpointConfig.Name
}

func (client *PoolClient) GetVersion() string {
	return client.versionStr
}

func (client *PoolClient) GetEndpointConfig() *types.EndpointConfig {
	return client.endpointConfig
}

func (client *PoolClient) GetLastHead() (phase0.Slot, phase0.Root) {
	client.headMutex.RLock()
	defer client.headMutex.RUnlock()
	return client.headSlot, client.headRoot
}

func (client *PoolClient) GetLastError() error {
	return client.lastError
}

func (client *PoolClient) GetLastEventTime() time.Time {
	return client.lastEvent
}

func (client *PoolClient) GetStatus() ClientStatus {
	if client.isSyncing {
		return ClientStatusSynchronizing
	} else if client.isOptimistic {
		return ClientStatusOptimistic
	} else if client.isOnline {
		return ClientStatusOnline
	} else {
		return ClientStatusOffline
	}
}
