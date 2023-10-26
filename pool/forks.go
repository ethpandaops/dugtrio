package pool

import (
	"bytes"
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type HeadFork struct {
	Slot         phase0.Slot
	Root         phase0.Root
	ReadyClients []*PoolClient
	AllClients   []*PoolClient
}

func (pool *BeaconPool) resetHeadForkCache() {
	pool.forkCacheMutex.Lock()
	defer pool.forkCacheMutex.Unlock()
	pool.forkCache = nil
}

func (pool *BeaconPool) GetCanonicalFork() *HeadFork {
	forks := pool.GetHeadForks()
	if len(forks) == 0 {
		return nil
	}
	return forks[0]
}

func (pool *BeaconPool) GetHeadForks() []*HeadFork {
	pool.forkCacheMutex.Lock()
	defer pool.forkCacheMutex.Unlock()
	if pool.forkCache != nil {
		return pool.forkCache
	}

	headForks := []*HeadFork{}
	for _, client := range pool.clients {
		cHeadSlot, cHeadRoot := client.GetLastHead()
		var matchingFork *HeadFork
		for _, fork := range headForks {
			if bytes.Equal(fork.Root[:], cHeadRoot[:]) || pool.blockCache.IsCanonicalBlock(cHeadRoot, fork.Root) {
				matchingFork = fork
				break
			}
			if pool.blockCache.IsCanonicalBlock(fork.Root, cHeadRoot) {
				fork.Root = cHeadRoot
				fork.Slot = cHeadSlot
				matchingFork = fork
				break
			}
		}
		if matchingFork == nil {
			matchingFork = &HeadFork{
				Root:       cHeadRoot,
				Slot:       cHeadSlot,
				AllClients: []*PoolClient{client},
			}
			headForks = append(headForks, matchingFork)
		} else {
			matchingFork.AllClients = append(matchingFork.AllClients, client)
		}
	}
	for _, fork := range headForks {
		fork.ReadyClients = make([]*PoolClient, 0)
		for _, client := range fork.AllClients {
			if client.GetStatus() != ClientStatusOnline {
				continue
			}
			var headDistance uint64 = 0
			_, cHeadRoot := client.GetLastHead()
			if !bytes.Equal(fork.Root[:], cHeadRoot[:]) {
				_, headDistance = pool.blockCache.GetBlockDistance(cHeadRoot, fork.Root)
			}
			if headDistance < 2 {
				fork.ReadyClients = append(fork.ReadyClients, client)
			}
		}
	}

	// sort by relevance (client count)
	sort.Slice(headForks, func(a, b int) bool {
		countA := len(headForks[a].ReadyClients)
		countB := len(headForks[b].ReadyClients)
		return countA > countB
	})

	pool.forkCache = headForks
	return headForks
}

func (fork *HeadFork) IsClientReady(client *PoolClient) bool {
	if fork == nil {
		return false
	}
	for _, cli := range fork.ReadyClients {
		if cli.clientIdx == client.clientIdx {
			return true
		}
	}
	return false
}
