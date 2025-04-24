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
	totalCalls   prometheus.Counter
	clientCalls  *prometheus.CounterVec
	pathCalls    *prometheus.CounterVec
	callDuration *prometheus.HistogramVec
	callStatus   *prometheus.CounterVec
}

func NewProxyMetrics(beaconPool *pool.BeaconPool) *ProxyMetrics {
	proxyMetrics := &ProxyMetrics{
		totalCalls: promauto.NewCounter(prometheus.CounterOpts{
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
		callDuration: promauto.NewHistogramVec(
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
	}

	err := prometheus.Register(proxyMetrics.totalCalls)
	if err != nil {
		logrus.Errorf("error registering total calls metric: %v", err)
	}

	err = prometheus.Register(proxyMetrics.clientCalls)
	if err != nil {
		logrus.Errorf("error registering client calls metric: %v", err)
	}

	err = prometheus.Register(proxyMetrics.pathCalls)
	if err != nil {
		logrus.Errorf("error registering path calls metric: %v", err)
	}

	err = prometheus.Register(proxyMetrics.callDuration)
	if err != nil {
		logrus.Errorf("error registering call duration metric: %v", err)
	}

	err = prometheus.Register(proxyMetrics.callStatus)
	if err != nil {
		logrus.Errorf("error registering call status metric: %v", err)
	}

	err = prometheus.Register(prometheus.NewGaugeFunc(
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
	))
	if err != nil {
		logrus.Errorf("error registering pool online metric: %v", err)
	}

	return proxyMetrics
}

func (proxyMetrics *ProxyMetrics) AddCall(clientName, apiPath string, callDuration time.Duration, callStatus int) {
	trimmedPath := proxyMetrics.trimAPIPath(apiPath)

	proxyMetrics.totalCalls.Inc()
	proxyMetrics.clientCalls.With(prometheus.Labels{
		"client": clientName,
	}).Inc()
	proxyMetrics.pathCalls.With(prometheus.Labels{
		"path": trimmedPath,
	}).Inc()
	proxyMetrics.callDuration.With(prometheus.Labels{
		"client": clientName,
		"path":   trimmedPath,
	}).Observe(float64(callDuration.Milliseconds()) / 1000)
	proxyMetrics.callStatus.With(prometheus.Labels{
		"client": clientName,
		"path":   trimmedPath,
		"status": fmt.Sprintf("%v", callStatus),
	}).Inc()
}

func (proxyMetrics *ProxyMetrics) trimAPIPath(apiPath string) string {
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
