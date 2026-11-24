package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dugtrio/types"
)

func TestBlockFinalizedStates(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{BlockFinalizedStates: true})

	blockCache := beaconPool.GetBlockCache()

	if err := blockCache.SetClientSpecs(map[string]interface{}{"SLOTS_PER_EPOCH": uint64(32)}); err != nil {
		t.Fatalf("error setting specs: %v", err)
	}

	blockCache.SetFinalizedCheckpoint(phase0.Epoch(100), phase0.Root{})

	tests := []struct {
		path    string
		blocked bool
	}{
		{"/eth/v1/beacon/states/3167/validators", true},
		{"/eth/v1/beacon/states/3168/validators", false},
		{"/eth/v1/beacon/states/3300/validators", false},
		{"/eth/v2/debug/beacon/states/100", true},
		{"/eth/v2/debug/beacon/states/3200", false},
		{"/eth/v1/beacon/states/head/validators", false},
		{"/eth/v1/beacon/states/finalized/finality_checkpoints", false},
		{"/eth/v1/beacon/states/genesis/root", false},
		{"/eth/v1/beacon/states/0x0000000000000000000000000000000000000000000000000000000000000001/root", false},
		{"/eth/v1/beacon/rewards/blocks/3167", true},
		{"/eth/v1/beacon/rewards/blocks/3168", false},
		{"/eth/v1/beacon/rewards/sync_committee/42", true},
		{"/eth/v1/beacon/rewards/attestations/98", true},
		{"/eth/v1/beacon/rewards/attestations/99", false},
		{"/eth/v2/beacon/blocks/42", false},
		{"/eth/v1/beacon/blocks/42/attestations", false},
		{"/eth/v1/beacon/headers/42", false},
		{"/eth/v1/beacon/headers?slot=42", false},
		{"/eth/v1/beacon/blob_sidecars/42", false},
		{"/eth/v1/beacon/execution_payload_envelopes/42", false},
		{"/eth/v1/beacon/states/%34%32/validators", true},
		{"/eth/v1/beacon/states/head/validators/42", false},
		{"/eth/v1/beacon/states/42/validators/3300", true},
		{"/eth/v1/beacon/states/head/committees?epoch=98", true},
		{"/eth/v1/beacon/states/head/committees?epoch=99", false},
		{"/eth/v1/beacon/states/head/committees?slot=3167", true},
		{"/eth/v1/beacon/states/head/sync_committees?epoch=3", true},
		{"/eth/v1/validator/duties/proposer/98", true},
		{"/eth/v1/validator/duties/proposer/101", false},
		{"/eth/v1/validator/duties/attester/50", true},
		{"/eth/v1/validator/liveness/101", false},
		{"/eth/v1/validator/liveness/99", false},
		{"/eth/v1/validator/liveness/98", true},
		{"/eth/v1/debug/beacon/data_column_sidecars/42", false},
		{"/eth/v1/validator/execution_payload_envelopes/3300/0x0000000000000000000000000000000000000000000000000000000000000001", false},
		{"/eth/v1/validator/attestation_data?slot=3300&committee_index=1", false},
		{"/eth/v1/node/syncing", false},
		{"/eth/v1/config/spec", false},
		{"/teku/v1/beacon/states/42", false},
	}

	for _, test := range tests {
		r := httptest.NewRequest(http.MethodGet, test.path, http.NoBody)

		if blocked := proxy.checkFinalizedStateQuery(r.URL); blocked != test.blocked {
			t.Errorf("%v: blocked = %v, want %v", test.path, blocked, test.blocked)
		}
	}

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/eth/v1/beacon/states/42/validators", http.NoBody))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %v, want %v", w.Code, http.StatusForbidden)
	}
}

func TestBlockFinalizedStatesDisabled(t *testing.T) {
	proxy, beaconPool := newTestProxy(t, &types.ProxyConfig{})

	blockCache := beaconPool.GetBlockCache()

	if err := blockCache.SetClientSpecs(map[string]interface{}{"SLOTS_PER_EPOCH": uint64(32)}); err != nil {
		t.Fatalf("error setting specs: %v", err)
	}

	blockCache.SetFinalizedCheckpoint(phase0.Epoch(100), phase0.Root{})

	r := httptest.NewRequest(http.MethodGet, "/eth/v1/beacon/states/42/validators", http.NoBody)

	if proxy.checkFinalizedStateQuery(r.URL) {
		t.Errorf("query blocked while blockFinalizedStates is disabled")
	}
}
