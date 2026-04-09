package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

type ProxyMetrics struct {
	totalCalls      prometheus.Counter
	clientCalls     *prometheus.CounterVec
	pathCalls       *prometheus.CounterVec
	callDuration    *prometheus.HistogramVec
	callStatus      *prometheus.CounterVec
	billingRequests *prometheus.CounterVec
}

// NewProxyMetrics registers all metrics against prometheus.DefaultRegisterer.
func NewProxyMetrics(beaconPool *pool.BeaconPool) *ProxyMetrics {
	return newProxyMetrics(beaconPool, prometheus.DefaultRegisterer)
}

// newProxyMetrics registers all metrics against the supplied registerer.
// Pass a fresh prometheus.NewRegistry() in tests to prevent cross-test pollution.
func newProxyMetrics(beaconPool *pool.BeaconPool, reg prometheus.Registerer) *ProxyMetrics {
	factory := promauto.With(reg)

	m := &ProxyMetrics{
		totalCalls: factory.NewCounter(prometheus.CounterOpts{
			Name: "dugtrio_calls_total",
			Help: "The total number of proxy requests",
		}),
		clientCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_client_calls_total",
				Help: "Number of proxy requests per client.",
			},
			[]string{"client"},
		),
		pathCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_path_calls_total",
				Help: "Number of proxy requests per api path.",
			},
			[]string{"path"},
		),
		callDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "dugtrio_call_time",
				Help: "Duration of proxy requests.",
			},
			[]string{"client", "path"},
		),
		callStatus: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_call_status_total",
				Help: "Number of requests per pool client.",
			},
			[]string{"client", "path", "status"},
		),
		billingRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dugtrio_billing_requests_total",
				Help: "Number of proxy requests per billing code, API path, HTTP method, and outcome status.",
			},
			[]string{"billing_code", "path", "method", "status"},
		),
	}

	for _, c := range []prometheus.Collector{
		m.clientCalls,
		m.pathCalls,
		m.callStatus,
		m.billingRequests,
	} {
		if err := reg.Register(c); err != nil {
			logrus.Errorf("error registering metric: %v", err)
		}
	}

	if beaconPool != nil {
		if err := reg.Register(prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Name: "dugtrio_pool_online",
				Help: "Number of online clients in the node pool.",
			},
			func() float64 {
				canonicalFork := beaconPool.GetCanonicalFork()
				if canonicalFork == nil {
					return 0
				}

				return float64(len(canonicalFork.ReadyClients))
			},
		)); err != nil {
			logrus.Errorf("error registering pool online metric: %v", err)
		}
	}

	return m
}

func (m *ProxyMetrics) AddCall(clientName, apiPath string, callDuration time.Duration, callStatus int) {
	trimmedPath := m.trimAPIPath(apiPath)

	m.totalCalls.Inc()
	m.clientCalls.With(prometheus.Labels{
		"client": clientName,
	}).Inc()
	m.pathCalls.With(prometheus.Labels{
		"path": trimmedPath,
	}).Inc()
	m.callDuration.With(prometheus.Labels{
		"client": clientName,
		"path":   trimmedPath,
	}).Observe(float64(callDuration.Milliseconds()) / 1000)
	m.callStatus.With(prometheus.Labels{
		"client": clientName,
		"path":   trimmedPath,
		"status": fmt.Sprintf("%v", callStatus),
	}).Inc()
}

// AddBillingCall records a completed proxy request attributed to a downstream
// billing code. path should already be normalized by NormalizePath.
// status is one of: "success", "upstream_error", "blocked", "no_upstream".
func (m *ProxyMetrics) AddBillingCall(billingCode, path, method, status string) {
	m.billingRequests.With(prometheus.Labels{
		"billing_code": billingCode,
		"path":         path,
		"method":       method,
		"status":       status,
	}).Inc()
}

func (m *ProxyMetrics) trimAPIPath(apiPath string) string {
	if queryPos := strings.Index(apiPath, "?"); queryPos > -1 {
		apiPath = apiPath[:queryPos]
	}

	pathParts := strings.Split(apiPath, "/")

	for i, pathPart := range pathParts {
		if i < 2 {
			continue
		}

		if strings.HasPrefix(pathPart, "0x") {
			pathParts[i] = "{hex}"
			continue
		}

		_, err := strconv.ParseUint(pathPart, 10, 64)
		if err == nil {
			pathParts[i] = "{id}"
			continue
		}
	}

	return strings.Join(pathParts, "/")
}
