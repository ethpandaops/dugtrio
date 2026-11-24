package proxy

import (
	"net/url"
	"regexp"
	"strconv"
)

const finalizedStateGraceEpochs = 1

var (
	stateSlotPathPattern  = regexp.MustCompile(`^/eth/v\d+/(?:debug/)?beacon/states/(\d+)(?:/|$)`)
	rewardSlotPathPattern = regexp.MustCompile(`^/eth/v\d+/beacon/rewards/(?:blocks|sync_committee)/(\d+)/?$`)
	rewardEpochPattern    = regexp.MustCompile(`^/eth/v\d+/beacon/rewards/attestations/(\d+)/?$`)
)

func (proxy *BeaconProxy) checkFinalizedStateQuery(reqURL *url.URL) bool {
	if !proxy.config.BlockFinalizedStates {
		return false
	}

	blockCache := proxy.pool.GetBlockCache()

	specs := blockCache.GetSpecs()
	if specs == nil || specs.SlotsPerEpoch == 0 {
		return false
	}

	finalizedEpoch, _ := blockCache.GetFinalizedCheckpoint()
	if uint64(finalizedEpoch) <= finalizedStateGraceEpochs {
		return false
	}

	minEpoch := uint64(finalizedEpoch) - finalizedStateGraceEpochs
	path := reqURL.EscapedPath()

	if match := rewardEpochPattern.FindStringSubmatch(path); match != nil {
		epoch, err := strconv.ParseUint(match[1], 10, 64)
		return err != nil || epoch < minEpoch
	}

	match := stateSlotPathPattern.FindStringSubmatch(path)
	if match == nil {
		match = rewardSlotPathPattern.FindStringSubmatch(path)
	}

	if match == nil {
		return false
	}

	slot, err := strconv.ParseUint(match[1], 10, 64)

	return err != nil || slot < minEpoch*specs.SlotsPerEpoch
}
