package pool

import (
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type CachedBlock struct {
	Root        phase0.Root
	Slot        phase0.Slot
	headerMutex sync.Mutex
	header      *phase0.SignedBeaconBlockHeader
	seenMutex   sync.RWMutex
	seenMap     map[uint16]bool
}

func (block *CachedBlock) SetSeenBy(clientIndex uint16) {
	block.seenMutex.Lock()
	defer block.seenMutex.Unlock()
	block.seenMap[clientIndex] = true
}

func (block *CachedBlock) GetHeader() *phase0.SignedBeaconBlockHeader {
	return block.header
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
