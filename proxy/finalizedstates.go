package proxy

import (
	"net/url"
	"strconv"
	"strings"
)

const finalizedStateGraceEpochs = 1

var (
	epochResources = map[string]bool{
		"attestations": true,
		"proposer":     true,
		"attester":     true,
		"sync":         true,
		"ptc":          true,
	}
	storedResources = map[string]bool{
		"blocks":                      true,
		"blinded_blocks":              true,
		"headers":                     true,
		"blob_sidecars":               true,
		"blobs":                       true,
		"execution_payload_envelopes": true,
	}
	indexResources = map[string]bool{
		"validators":         true,
		"validator_balances": true,
	}
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

	minSlot := (uint64(finalizedEpoch) - finalizedStateGraceEpochs) * specs.SlotsPerEpoch

	slot, found := oldestRequestedSlot(reqURL, specs.SlotsPerEpoch)

	return found && slot < minSlot
}

func oldestRequestedSlot(reqURL *url.URL, slotsPerEpoch uint64) (oldest uint64, found bool) {
	segments := strings.Split(strings.Trim(reqURL.Path, "/"), "/")
	if segments[0] != "eth" {
		return 0, false
	}

	track := func(value string, isEpoch bool) {
		slot, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return
		}

		if isEpoch {
			slot *= slotsPerEpoch
		}

		if !found || slot < oldest {
			oldest, found = slot, true
		}
	}

	stored := func(i int) bool {
		return storedResources[segments[i]] && segments[i-1] != "rewards"
	}

	for i := 2; i < len(segments); i++ {
		if stored(i-1) || indexResources[segments[i-1]] {
			continue
		}

		track(segments[i], epochResources[segments[i-1]])
	}

	if len(segments) > 1 && !stored(len(segments)-1) {
		query := reqURL.Query()
		track(query.Get("slot"), false)
		track(query.Get("epoch"), true)
	}

	return oldest, found
}
