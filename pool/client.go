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

type Client struct {
	beaconPool        *BeaconPool
	clientIdx         uint16
	endpointConfig    *types.EndpointConfig
	clientCtx         context.Context
	clientCtxCancel   context.CancelFunc
	rpcClient         *rpc.BeaconClient
	logger            *logrus.Entry
	isOnline          bool
	isSyncing         bool
	isOptimistic      bool
	custodyGroupCount uint16
	versionStr        string
	clientType        ClientType
	lastEvent         time.Time
	lastSyncCheck     time.Time
	lastMetaDataCheck time.Time
	retryCounter      uint64
	lastError         error
	headMutex         sync.RWMutex
	headRoot          phase0.Root
	headSlot          phase0.Slot
	finalizedRoot     phase0.Root
	finalizedEpoch    phase0.Epoch
}

func (pool *BeaconPool) newPoolClient(clientIdx uint16, endpoint *types.EndpointConfig) (*Client, error) {
	rpcClient, err := rpc.NewBeaconClient(endpoint)
	if err != nil {
		return nil, err
	}

	client := Client{
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

func (client *Client) resetContext() {
	if client.clientCtxCancel != nil {
		client.clientCtxCancel()
	}

	client.clientCtx, client.clientCtxCancel = context.WithCancel(context.Background())
}

func (client *Client) GetIndex() uint16 {
	return client.clientIdx
}

func (client *Client) GetName() string {
	return client.endpointConfig.Name
}

func (client *Client) GetVersion() string {
	return client.versionStr
}

func (client *Client) GetEndpointConfig() *types.EndpointConfig {
	return client.endpointConfig
}

func (client *Client) GetLastHead() (phase0.Slot, phase0.Root) {
	client.headMutex.RLock()
	defer client.headMutex.RUnlock()

	return client.headSlot, client.headRoot
}

func (client *Client) GetLastError() error {
	return client.lastError
}

func (client *Client) GetLastEventTime() time.Time {
	return client.lastEvent
}

func (client *Client) GetStatus() ClientStatus {
	switch {
	case client.isSyncing:
		return ClientStatusSynchronizing
	case client.isOptimistic:
		return ClientStatusOptimistic
	case client.isOnline:
		return ClientStatusOnline
	default:
		return ClientStatusOffline
	}
}

func (client *Client) GetCustodyGroupCount() uint16 {
	return client.custodyGroupCount
}
