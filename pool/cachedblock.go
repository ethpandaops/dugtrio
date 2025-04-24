package pool

import (
	"sort"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type CachedBlock struct {
	Root        phase0.Root
	Slot        phase0.Slot
	headerMutex sync.Mutex
	header      *phase0.SignedBeaconBlockHeader
	seenMutex   sync.RWMutex
	seenMap     map[uint16]*PoolClient
}

func (block *CachedBlock) GetSeenBy() []*PoolClient {
	block.seenMutex.RLock()
	defer block.seenMutex.RUnlock()

	clients := []*PoolClient{}
	for _, client := range block.seenMap {
		clients = append(clients, client)
	}

	sort.Slice(clients, func(a, b int) bool {
		return clients[a].clientIdx < clients[b].clientIdx
	})

	return clients
}

func (block *CachedBlock) SetSeenBy(client *PoolClient) {
	block.seenMutex.Lock()
	defer block.seenMutex.Unlock()

	block.seenMap[client.clientIdx] = client
}

func (block *CachedBlock) GetHeader() *phase0.SignedBeaconBlockHeader {
	return block.header
}

func (block *CachedBlock) GetParentRoot() *phase0.Root {
	if block.header == nil {
		return nil
	}

	return &block.header.Message.ParentRoot
}

func (block *CachedBlock) SetHeader(header *phase0.SignedBeaconBlockHeader) {
	block.header = header
}

func (block *CachedBlock) EnsureHeader(loadHeader func() (*phase0.SignedBeaconBlockHeader, error)) error {
	if block.header != nil {
		return nil
	}

	block.headerMutex.Lock()
	defer block.headerMutex.Unlock()

	if block.header != nil {
		return nil
	}

	header, err := loadHeader()
	if err != nil {
		return err
	}

	block.header = header
	return nil
}
