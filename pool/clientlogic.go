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

func (client *Client) runPoolClientLoop() {
	defer utils.HandleSubroutinePanic("Client.runPoolClientLoop", client.runPoolClientLoop)

	for {
		err := client.checkPoolClient()

		if err == nil {
			err = client.runPoolClient()
		}

		if err == nil {
			client.retryCounter = 0
			return
		}

		client.updateStatus(false, client.isSyncing, client.isOptimistic)
		client.lastError = err
		client.lastEvent = time.Now()
		client.retryCounter++

		time.Sleep(10 * time.Second)
	}
}

func (client *Client) checkPoolClient() error {
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

	client.parseClientVersion(nodeVersion)

	// get & comare chain specs
	specs, err := client.rpcClient.GetConfigSpecs(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching specs: %v", err)
	}

	err = client.beaconPool.blockCache.SetClientSpecs(specs)
	if err != nil {
		return fmt.Errorf("invalid node specs: %v", err)
	}

	err = client.checkSyncStatus()
	if err != nil {
		return err
	}

	return nil
}

func (client *Client) checkSyncStatus() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// check synchronization state
	syncStatus, err := client.rpcClient.GetNodeSyncing(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching synchronization status: %v", err)
	}

	if syncStatus == nil {
		return fmt.Errorf("could not get synchronization status")
	}

	client.lastSyncCheck = time.Now()
	client.updateStatus(client.isOnline, syncStatus.IsSyncing, syncStatus.IsOptimistic)

	return nil
}

func (client *Client) runPoolClient() error {
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

	specs := client.beaconPool.blockCache.GetSpecs()
	finalizedEpoch, _ := client.beaconPool.blockCache.GetFinalizedCheckpoint()

	if client.headSlot < phase0.Slot(finalizedEpoch)*phase0.Slot(specs.SlotsPerEpoch) {
		return fmt.Errorf("beacon node is behind finalized checkpoint (node head: %v, finalized: %v)", client.headSlot, phase0.Slot(finalizedEpoch)*phase0.Slot(specs.SlotsPerEpoch))
	}

	// start event stream
	blockStream := client.rpcClient.NewBlockStream(rpc.StreamBlockEvent | rpc.StreamFinalizedEvent)
	defer blockStream.Close()

	// process events
	client.lastEvent = time.Now()

	for {
		syncCheckTimeout := time.Since(client.lastSyncCheck)
		if syncCheckTimeout > 30*time.Second {
			syncCheckTimeout = 0
		} else {
			syncCheckTimeout = 30*time.Second - syncCheckTimeout
		}

		eventTimeout := time.Since(client.lastEvent)
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
				blockEvent, ok := evt.Data.(*v1.BlockEvent)
				if !ok {
					client.logger.Warnf("invalid block event: %v", evt.Data)
					continue
				}

				err = client.processBlockEvent(blockEvent)
				if err != nil {
					client.logger.Warnf("error processing block event: %v", err)
				}
			case rpc.StreamFinalizedEvent:
				checkpointEvent, ok := evt.Data.(*v1.FinalizedCheckpointEvent)
				if !ok {
					client.logger.Warnf("invalid finalized checkpoint event: %v", evt.Data)
					continue
				}

				err = client.processFinalizedEvent(checkpointEvent)
				if err != nil {
					client.logger.Warnf("error processing finalized event: %v", err)
				}
			}

			client.logger.Tracef("event (%v) processing time: %v ms", evt.Event, time.Since(now).Milliseconds())
			client.lastEvent = time.Now()
		case ready := <-blockStream.ReadyChan:
			if client.isOnline != ready {
				client.updateStatus(ready, client.isSyncing, client.isOptimistic)

				if ready {
					client.logger.Debug("RPC event stream connected")
				} else {
					client.logger.Debug("RPC event stream disconnected")
				}
			}
		case <-time.After(syncCheckTimeout):
			err := client.checkSyncStatus()
			if err != nil {
				client.updateStatus(false, client.isSyncing, client.isOptimistic)
				return err
			}
		case <-time.After(eventTimeout):
			client.logger.Debug("no head event since 30 secs, polling chain head")

			err := client.pollClientHead()
			if err != nil {
				client.updateStatus(false, client.isSyncing, client.isOptimistic)
				return err
			}

			client.lastEvent = time.Now()
		}
	}
}

func (client *Client) updateStatus(online, syncing, optimistic bool) {
	oldStatus := client.GetStatus()

	client.isOnline = online
	client.isSyncing = syncing
	client.isOptimistic = optimistic

	newStatus := client.GetStatus()
	if oldStatus != newStatus {
		client.logger.Infof("status changed  %v -> %v", oldStatus, newStatus)
		client.beaconPool.resetHeadForkCache()
	}
}

func (client *Client) processBlockEvent(evt *v1.BlockEvent) error {
	currentBlock, isNewBlock := client.beaconPool.blockCache.AddBlock(evt.Block, evt.Slot)
	if currentBlock != nil {
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

		currentBlock.SetSeenBy(client)
	}

	client.headMutex.Lock()
	client.headSlot = evt.Slot
	client.headRoot = evt.Block
	client.headMutex.Unlock()
	client.beaconPool.resetHeadForkCache()

	return nil
}

func (client *Client) processFinalizedEvent(evt *v1.FinalizedCheckpointEvent) error {
	client.logger.Debugf("received finalization_checkpoint event: finalized %v [0x%x]", evt.Epoch, evt.Block)
	client.setFinalizedHead(evt.Epoch, evt.Block)

	return nil
}

func (client *Client) pollClientHead() error {
	ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
	defer cancel()

	latestHeader, err := client.rpcClient.GetLatestBlockHead(ctx)
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}

	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}

	err = client.setHeader(latestHeader.Root, latestHeader.Header)
	if err != nil {
		return fmt.Errorf("could not set header: %v", err)
	}

	finalityCheckpoint, err := client.rpcClient.GetFinalityCheckpoints(ctx)
	if err != nil {
		return fmt.Errorf("could not get finality checkpoint: %v", err)
	}

	client.setFinalizedHead(finalityCheckpoint.Finalized.Epoch, finalityCheckpoint.Finalized.Root)

	return nil
}

func (client *Client) setHeader(root phase0.Root, header *phase0.SignedBeaconBlockHeader) error {
	cachedBlock, _ := client.beaconPool.blockCache.AddBlock(root, header.Message.Slot)
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
	client.beaconPool.resetHeadForkCache()

	return nil
}

func (client *Client) setFinalizedHead(epoch phase0.Epoch, root phase0.Root) {
	client.headMutex.Lock()

	if bytes.Equal(client.finalizedRoot[:], root[:]) {
		client.headMutex.Unlock()
	}

	client.finalizedEpoch = epoch
	client.finalizedRoot = root

	client.headMutex.Unlock()
	client.beaconPool.blockCache.SetFinalizedCheckpoint(epoch, root)
}
