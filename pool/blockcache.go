package pool

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/mashingan/smapping"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dugtrio/types"
	"github.com/ethpandaops/dugtrio/utils"
)

type BlockCache struct {
	followDistance uint64
	specMutex      sync.RWMutex
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
	go cache.runCacheCleanup()
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
		seenMap: make(map[uint16]*PoolClient),
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

func (cache *BlockCache) GetCachedBlockByRoot(root phase0.Root) *CachedBlock {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()
	return cache.rootMap[root]
}

func (cache *BlockCache) GetCachedBlocks() []*CachedBlock {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	blocks := []*CachedBlock{}
	slots := []phase0.Slot{}
	for slot := range cache.slotMap {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(a, b int) bool {
		return slots[a] > slots[b]
	})

	for _, slot := range slots {
		blocks = append(blocks, cache.slotMap[slot]...)
	}
	return blocks
}

func (cache *BlockCache) runCacheCleanup() {
	defer utils.HandleSubroutinePanic("pool.blockcache.cleanup")

	for {
		time.Sleep(30 * time.Second)

		err := cache.cleanupCache()
		if err != nil {
			logrus.Errorf("error during cache cleanup: %v", err)
		}
	}
}

func (cache *BlockCache) cleanupCache() error {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	minSlot := cache.maxSlotIdx - int64(cache.followDistance)
	if minSlot <= 0 {
		return nil
	}
	for slot, blocks := range cache.slotMap {
		if slot >= phase0.Slot(minSlot) {
			continue
		}

		for _, block := range blocks {
			delete(cache.rootMap, block.Root)
		}
		delete(cache.slotMap, slot)
	}
	return nil
}

func (cache *BlockCache) IsCanonicalBlock(blockRoot phase0.Root, headRoot phase0.Root) bool {
	res, _ := cache.GetBlockDistance(blockRoot, headRoot)
	return res
}

func (cache *BlockCache) GetBlockDistance(blockRoot phase0.Root, headRoot phase0.Root) (bool, uint64) {
	if bytes.Equal(headRoot[:], blockRoot[:]) {
		return true, 0
	}
	block := cache.GetCachedBlockByRoot(blockRoot)
	if block == nil {
		return false, 0
	}
	blockSlot := block.Slot
	headBlock := cache.GetCachedBlockByRoot(headRoot)
	var distance uint64 = 0
	for headBlock != nil {
		if headBlock.Slot < blockSlot {
			return false, 0
		}
		parentRoot := headBlock.GetParentRoot()
		if parentRoot == nil {
			return false, 0
		}
		distance++
		if bytes.Equal(parentRoot[:], blockRoot[:]) {
			return true, distance
		}
		headBlock = cache.GetCachedBlockByRoot(*parentRoot)
	}
	return false, 0
}
