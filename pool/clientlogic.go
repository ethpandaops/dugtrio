package pool

import (
	"bytes"
	"context"
	"fmt"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dugtrio/rpc"
	"github.com/ethpandaops/dugtrio/utils"
)

func (client *PoolClient) runUpstreamClientLoop() {
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
		client.lastEvent = time.Now()
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

func (client *PoolClient) checkUpstreamClient() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err := client.rpcClient.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialization of attestantio/go-eth2-client failed: %w", err)
	}

	specs, err := client.rpcClient.GetConfigSpecs(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching specs: %v", err)
	}
	err = client.blockCache.SetClientSpecs(specs)
	if err != nil {
		return fmt.Errorf("invalid node specs: %v", err)
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

func (client *PoolClient) runUpstreamClient() error {
	// get latest header
	err := client.pollClientHead()
	if err != nil {
		return err
	}

	// check latest header / sync status
	if client.isSyncing {
		return fmt.Errorf("beacon node is synchronizing")
	}
	if client.isOptimistic {
		return fmt.Errorf("beacon node is optimistic")
	}

	specs := client.blockCache.GetSpecs()
	finalizedEpoch, _ := client.blockCache.GetFinalizedCheckpoint()
	if client.headSlot < phase0.Slot(finalizedEpoch)*phase0.Slot(specs.SlotsPerEpoch) {
		return fmt.Errorf("beacon node is behind finalized checkpoint (node head: %v, finalized: %v)", client.headSlot, phase0.Slot(finalizedEpoch)*phase0.Slot(specs.SlotsPerEpoch))
	}

	// start event stream
	blockStream := client.rpcClient.NewBlockStream(rpc.StreamBlockEvent | rpc.StreamFinalizedEvent)
	defer blockStream.Close()

	// process events
	client.lastEvent = time.Now()
	for {
		var eventTimeout time.Duration = time.Since(client.lastEvent)
		if eventTimeout > 30*time.Second {
			eventTimeout = 0
		} else {
			eventTimeout = 30*time.Second - eventTimeout
		}
		select {
		case evt := <-blockStream.EventChan:
			now := time.Now()
			switch evt.Event {
			case rpc.StreamBlockEvent:
				client.processBlockEvent(evt.Data.(*v1.BlockEvent))
			case rpc.StreamFinalizedEvent:
				client.processFinalizedEvent(evt.Data.(*v1.FinalizedCheckpointEvent))
			}
			client.logger.Tracef("event (%v) processing time: %v ms", evt.Event, time.Since(now).Milliseconds())
			client.lastEvent = time.Now()
		case ready := <-blockStream.ReadyChan:
			if client.isOnline != ready {
				client.isOnline = ready
				if ready {
					client.logger.Debug("RPC event stream connected")
				} else {
					client.logger.Debug("RPC event stream disconnected")
				}
			}
		case <-time.After(eventTimeout):
			client.logger.Debug("no head event since 30 secs, polling chain head")
			err := client.pollClientHead()
			if err != nil {
				client.isOnline = false
				return err
			}
			client.lastEvent = time.Now()
		}
	}
}

func (client *PoolClient) processBlockEvent(evt *v1.BlockEvent) error {
	currentBlock, isNewBlock := client.blockCache.AddBlock(evt.Block, evt.Slot)
	if isNewBlock {
		client.logger.Infof("received block %v [0x%x] stream", currentBlock.Slot, currentBlock.Root)
	} else {
		client.logger.Debugf("received known block %v [0x%x] stream", currentBlock.Slot, currentBlock.Root)
	}

	err := currentBlock.EnsureHeader(func() (*phase0.SignedBeaconBlockHeader, error) {
		ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
		defer cancel()
		header, err := client.rpcClient.GetBlockHeaderByBlockroot(ctx, currentBlock.Root)
		if err != nil {
			return nil, err
		}
		return header.Header, nil
	})
	if err != nil {
		return err
	}

	client.headMutex.Lock()
	client.headSlot = evt.Slot
	client.headRoot = evt.Block
	client.headMutex.Unlock()
	currentBlock.SetSeenBy(client)

	return nil
}

func (client *PoolClient) processFinalizedEvent(evt *v1.FinalizedCheckpointEvent) error {
	client.logger.Debugf("received finalization_checkpoint event: finalized %v [0x%x]", evt.Epoch, evt.Block)
	client.setFinalizedHead(evt.Epoch, evt.Block)
	return nil
}

func (client *PoolClient) pollClientHead() error {
	ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
	defer cancel()

	latestHeader, err := client.rpcClient.GetLatestBlockHead(ctx)
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}
	client.setHeader(latestHeader.Root, latestHeader.Header)

	finalityCheckpoint, err := client.rpcClient.GetFinalityCheckpoints(ctx)
	if err != nil {
		return fmt.Errorf("could not get finality checkpoint: %v", err)
	}
	client.setFinalizedHead(finalityCheckpoint.Finalized.Epoch, finalityCheckpoint.Finalized.Root)

	return nil
}

func (client *PoolClient) setHeader(root phase0.Root, header *phase0.SignedBeaconBlockHeader) error {
	cachedBlock, _ := client.blockCache.AddBlock(root, header.Message.Slot)
	if cachedBlock != nil {
		cachedBlock.SetHeader(header)
		cachedBlock.SetSeenBy(client)
	}

	client.headMutex.Lock()
	if bytes.Equal(client.headRoot[:], root[:]) {
		client.headMutex.Unlock()
		return nil
	}
	client.headSlot = header.Message.Slot
	client.headRoot = root
	client.headMutex.Unlock()

	return nil
}

func (client *PoolClient) setFinalizedHead(epoch phase0.Epoch, root phase0.Root) error {
	client.headMutex.Lock()
	if bytes.Equal(client.finalizedRoot[:], root[:]) {
		client.headMutex.Unlock()
		return nil
	}
	client.finalizedEpoch = epoch
	client.finalizedRoot = root
	client.headMutex.Unlock()

	client.blockCache.SetFinalizedCheckpoint(epoch, root)

	return nil
}
