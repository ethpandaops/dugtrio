package pool

import (
	"fmt"
	"strings"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/mashingan/smapping"

	"github.com/ethpandaops/dugtrio/types"
)

type BlockCache struct {
	followDistance uint64
	specMutex      sync.RWMutex
	specHash       uint64
	specs          *types.ChainConfig
	finalizedMutex sync.RWMutex
	finalizedEpoch phase0.Epoch
	finalizedRoot  phase0.Root
	cacheMutex     sync.RWMutex
	slotMap        map[phase0.Slot][]*CachedBlock
	rootMap        map[phase0.Root]*CachedBlock
	maxSlotIdx     int64
}

func NewBlockCache(followDistance uint64) (*BlockCache, error) {
	if followDistance == 0 {
		return nil, fmt.Errorf("cannot initialize block cache without follow distance")
	}
	cache := BlockCache{
		followDistance: followDistance,
		slotMap:        make(map[phase0.Slot][]*CachedBlock),
		rootMap:        make(map[phase0.Root]*CachedBlock),
	}
	return &cache, nil
}

func (cache *BlockCache) SetClientSpecs(specValues map[string]interface{}) error {
	cache.specMutex.Lock()
	defer cache.specMutex.Unlock()

	specs := types.ChainConfig{}
	err := smapping.FillStructByTags(&specs, specValues, "yaml")
	if err != nil {
		return err
	}

	if cache.specs != nil {
		mismatches := cache.specs.CheckMismatch(&specs)
		if len(mismatches) > 0 {
			return fmt.Errorf("spec mismatch: %v", strings.Join(mismatches, ", "))
		}
	}
	cache.specs = &specs

	return nil
}

func (cache *BlockCache) GetSpecs() *types.ChainConfig {
	cache.specMutex.RLock()
	defer cache.specMutex.RUnlock()
	return cache.specs
}

func (cache *BlockCache) SetFinalizedCheckpoint(finalizedEpoch phase0.Epoch, finalizedRoot phase0.Root) {
	cache.finalizedMutex.Lock()
	defer cache.finalizedMutex.Unlock()

	if finalizedEpoch > cache.finalizedEpoch {
		cache.finalizedEpoch = finalizedEpoch
		cache.finalizedRoot = finalizedRoot
	}
}

func (cache *BlockCache) GetFinalizedCheckpoint() (phase0.Epoch, phase0.Root) {
	cache.finalizedMutex.RLock()
	defer cache.finalizedMutex.RUnlock()

	return cache.finalizedEpoch, cache.finalizedRoot
}

func (cache *BlockCache) AddBlock(root phase0.Root, slot phase0.Slot) (*CachedBlock, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	if cache.rootMap[root] != nil {
		return cache.rootMap[root], false
	}
	if int64(slot) < cache.maxSlotIdx-int64(cache.followDistance) {
		return nil, false
	}
	cacheBlock := &CachedBlock{
		Root:    root,
		Slot:    slot,
		seenMap: make(map[uint16]bool),
	}
	cache.rootMap[root] = cacheBlock
	if cache.slotMap[slot] == nil {
		cache.slotMap[slot] = []*CachedBlock{cacheBlock}
	} else {
		cache.slotMap[slot] = append(cache.slotMap[slot], cacheBlock)
	}
	if int64(slot) > cache.maxSlotIdx {
		cache.maxSlotIdx = int64(slot)
	}
	return cacheBlock, true
}
