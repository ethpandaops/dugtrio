package proxy

import (
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"sync"
)

type validatorStats struct {
	validators map[uint64]uint8
	mu         sync.RWMutex
}

type ValidatorStats struct {
	Count      uint64
	Validators []ValidatorSlice
}

type ValidatorSlice struct {
	Start  uint64
	Length uint32
	Flag   uint8
}

func (session *ProxySession) GetValidatorStats() *ValidatorStats {
	resStats := &ValidatorStats{}
	if session.validatorStats == nil {
		return resStats
	}
	session.validatorStats.mu.RLock()
	defer session.validatorStats.mu.RUnlock()

	indexes := make([]uint64, 0, len(session.validatorStats.validators))
	for idx := range session.validatorStats.validators {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool {
		return indexes[i] < indexes[j]
	})

	var lastSlice *ValidatorSlice
	resStats.Count = uint64(len(indexes))
	resStats.Validators = make([]ValidatorSlice, 0, len(indexes))
	for _, idx := range indexes {
		flag := session.validatorStats.validators[idx]
		if flag == 0 {
			continue
		}

		if lastSlice == nil || lastSlice.Flag != flag || lastSlice.Start+uint64(lastSlice.Length) != idx {
			resStats.Validators = append(resStats.Validators, ValidatorSlice{
				Start:  idx,
				Length: 1,
				Flag:   flag,
			})
			lastSlice = &resStats.Validators[len(resStats.Validators)-1]
		} else {
			lastSlice.Length++
		}
	}

	return resStats
}

func (session *ProxySession) getOrCreateValidatorStats() *validatorStats {
	if session.validatorStats == nil {
		session.validatorStats = &validatorStats{
			validators: make(map[uint64]uint8),
		}
	}
	return session.validatorStats
}

func (proxy *BeaconProxy) analyzePrepareBeaconProposer(session *ProxySession, body io.ReadCloser) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	tee := io.TeeReader(body, pw)

	go func() {
		defer pw.Close()
		decoder := json.NewDecoder(tee)

		var registrations []struct {
			ValidatorIndex string `json:"validator_index"`
		}

		if err := decoder.Decode(&registrations); err == nil {
			stats := session.getOrCreateValidatorStats()
			stats.mu.Lock()
			for _, reg := range registrations {
				if idx, err := strconv.ParseUint(reg.ValidatorIndex, 10, 64); err == nil {
					stats.validators[idx] |= 0x01
				}
			}
			stats.mu.Unlock()
		}

		io.Copy(io.Discard, tee)
	}()

	return pr, nil
}

func (proxy *BeaconProxy) analyzeBeaconCommitteeSubscriptions(session *ProxySession, body io.ReadCloser) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	tee := io.TeeReader(body, pw)

	go func() {
		defer pw.Close()
		decoder := json.NewDecoder(tee)

		var subscriptions []struct {
			ValidatorIndex string `json:"validator_index"`
		}

		if err := decoder.Decode(&subscriptions); err == nil {
			stats := session.getOrCreateValidatorStats()
			stats.mu.Lock()
			for _, sub := range subscriptions {
				if idx, err := strconv.ParseUint(sub.ValidatorIndex, 10, 64); err == nil {
					stats.validators[idx] |= 0x02
				}
			}
			stats.mu.Unlock()
		}

		io.Copy(io.Discard, tee)
	}()

	return pr, nil
}
