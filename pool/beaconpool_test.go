package pool

import (
	"testing"

	"github.com/ethpandaops/dugtrio/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPool(t *testing.T) *BeaconPool {
	t.Helper()

	p, err := NewBeaconPool(&types.PoolConfig{
		FollowDistance:  10,
		MaxHeadDistance: 100,
		SchedulerMode:   "primary-fallback",
	})
	require.NoError(t, err)

	return p
}

func TestGetReadyEndpointExcluding_NilWhenNoClients(t *testing.T) {
	p := newTestPool(t)
	assert.Nil(t, p.GetReadyEndpointExcluding(UnspecifiedClient, nil))
}

func TestGetReadyEndpointExcluding_NilWhenNoCanonicalFork(t *testing.T) {
	p := newTestPool(t)
	_, err := p.AddEndpoint(&types.EndpointConfig{Name: "ep1", URL: "http://localhost:5052/"})
	require.NoError(t, err)

	// canonical fork is nil until health monitoring runs
	assert.Nil(t, p.GetReadyEndpointExcluding(UnspecifiedClient, nil))
}

func TestGetReadyEndpointExcluding_RespectsExcludeList(t *testing.T) {
	p := newTestPool(t)
	_, err := p.AddEndpoint(&types.EndpointConfig{Name: "ep1", URL: "http://localhost:5052/"})
	require.NoError(t, err)

	// with ep1 excluded and no canonical fork, should return nil
	assert.Nil(t, p.GetReadyEndpointExcluding(UnspecifiedClient, []string{"ep1"}))
}
