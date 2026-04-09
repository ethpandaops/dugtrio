package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddBillingCall_IncrementsCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newProxyMetrics(nil, reg)

	m.AddBillingCall("ACM-ETH-01", "/eth/v1/node/version", "GET", "success")
	m.AddBillingCall("ACM-ETH-01", "/eth/v1/node/version", "GET", "success")
	m.AddBillingCall("GLB-ETH-01", "/eth/v1/beacon/blocks/{id}", "GET", "upstream_error")

	count, err := testutil.GatherAndCount(reg, "dugtrio_billing_requests_total")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	val := testutil.ToFloat64(m.billingRequests.With(prometheus.Labels{
		"billing_code": "ACM-ETH-01",
		"path":         "/eth/v1/node/version",
		"method":       "GET",
		"status":       "success",
	}))
	assert.Equal(t, float64(2), val)
}
