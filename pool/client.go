package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dugtrio/rpc"
	"github.com/ethpandaops/dugtrio/types"
	"github.com/ethpandaops/dugtrio/utils"
)

type ClientStatus uint8

var (
	ClientStatusOnline        ClientStatus = 1
	ClientStatusOffline       ClientStatus = 2
	ClientStatusSynchronizing ClientStatus = 3
	ClientStatusOptimistic    ClientStatus = 4
)

type UpstreamClient struct {
	clientIdx       uint8
	endpointConfig  *types.EndpointConfig
	clientCtx       context.Context
	clientCtxCancel context.CancelFunc
	rpcClient       *rpc.BeaconClient
	logger          *logrus.Entry
	isOnline        bool
	isSyncing       bool
	isOptimistic    bool
	versionStr      string
	lastSeen        time.Time
	retryCounter    uint64
	lastError       error
	headMutex       sync.RWMutex
	headRoot        []byte
	headSlot        int64
}

func newUpstreamClient(clientIdx uint8, endpoint *types.EndpointConfig) (*UpstreamClient, error) {
	rpcClient, err := rpc.NewBeaconClient(endpoint)
	if err != nil {
		return nil, err
	}

	client := UpstreamClient{
		clientIdx:      clientIdx,
		endpointConfig: endpoint,
		rpcClient:      rpcClient,
		logger:         logrus.WithField("client", endpoint.Name),
		headSlot:       -1,
	}
	client.resetContext()
	go client.runUpstreamClientLoop()
	return &client, nil
}

func (client *UpstreamClient) resetContext() {
	if client.clientCtxCancel != nil {
		client.clientCtxCancel()
	}
	client.clientCtx, client.clientCtxCancel = context.WithCancel(context.Background())
}

func (client *UpstreamClient) GetIndex() uint8 {
	return client.clientIdx
}

func (client *UpstreamClient) GetName() string {
	return client.endpointConfig.Name
}

func (client *UpstreamClient) GetVersion() string {
	return client.versionStr
}

func (client *UpstreamClient) GetRpcClient() *rpc.BeaconClient {
	return client.rpcClient
}

func (client *UpstreamClient) GetLastHead() (int64, []byte) {
	client.headMutex.RLock()
	defer client.headMutex.RUnlock()
	return client.headSlot, client.headRoot
}

func (client *UpstreamClient) GetStatus() ClientStatus {
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

func (client *UpstreamClient) runUpstreamClientLoop() {
	defer utils.HandleSubroutinePanic("runUpstreamClientLoop")

	for {
		err := client.checkUpstreamClient()

		if err == nil {
			err = client.runUpstreamClient()
		}
		if err == nil {
			client.retryCounter = 0
			return
		}

		client.isOnline = false
		client.lastError = err
		client.retryCounter++
		waitTime := 10
		if client.retryCounter > 10 {
			waitTime = 300
		} else if client.retryCounter > 5 {
			waitTime = 60
		}

		client.logger.Warnf("upstream client error: %v, retrying in %v sec...", err, waitTime)
		time.Sleep(time.Duration(waitTime) * time.Second)
	}
}

func (client *UpstreamClient) checkUpstreamClient() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err := client.rpcClient.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialization of attestantio/go-eth2-client failed: %w", err)
	}

	// get node version
	nodeVersion, err := client.rpcClient.GetNodeVersion(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching node version: %v", err)
	}
	client.versionStr = nodeVersion

	// check syncronization state
	syncStatus, err := client.rpcClient.GetNodeSyncing(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching synchronization status: %v", err)
	}
	if syncStatus == nil {
		return fmt.Errorf("could not get synchronization status")
	}
	client.isSyncing = syncStatus.IsSyncing
	client.isOptimistic = syncStatus.IsOptimistic

	return nil
}

func (client *UpstreamClient) runUpstreamClient() error {
	// get latest header
	latestHeader, err := client.rpcClient.GetLatestBlockHead(client.clientCtx)
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}
	//headSlot := uint64(latestHeader.Header.Message.Slot)

	// check latest header / sync status
	if client.isSyncing {
		return fmt.Errorf("beacon node is synchronizing")
	}
	if client.isOptimistic {
		return fmt.Errorf("beacon node is optimistic")
	}

	return nil
}
