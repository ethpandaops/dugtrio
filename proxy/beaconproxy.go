package proxy

import (
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/dugtrio/metrics"
	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/types"
	"github.com/ethpandaops/dugtrio/utils"
	"github.com/sirupsen/logrus"
)

var passthruRequestHeaderKeys = [...]string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Cache-Control",
	"Content-Type",
	"Cookie",
	"Referer",
	"User-Agent",
	"Eth-Consensus-Version",
	"Eth-Consensus-Block-Value",
	"Eth-Consensus-Dependent-Root",
	"Eth-Execution-Payload-Value",
	"Eth-Execution-Payload-Blinded",
}

var passthruResponseHeaderKeys = [...]string{
	"Content-Encoding",
	"Content-Language",
	"Content-Type",
	"Date",
	"Etag",
	"Eth-Consensus-Version",
	"Eth-Consensus-Block-Value",
	"Eth-Consensus-Dependent-Root",
	"Eth-Execution-Payload-Blinded",
	"Eth-Execution-Payload-Value",
	"Expires",
	"Last-Modified",
	"Location",
	"Server",
	"Vary",
}

type BeaconProxy struct {
	config       *types.ProxyConfig
	pool         *pool.BeaconPool
	proxyMetrics *metrics.ProxyMetrics
	logger       *logrus.Entry
	blockedPaths []regexp.Regexp

	sessionMutex sync.Mutex
	sessions     map[string]*ProxySession
}

func NewBeaconProxy(config *types.ProxyConfig, pool *pool.BeaconPool, proxyMetrics *metrics.ProxyMetrics) (*BeaconProxy, error) {
	proxy := BeaconProxy{
		config:       config,
		pool:         pool,
		proxyMetrics: proxyMetrics,
		logger:       logrus.WithField("module", "proxy"),
		blockedPaths: []regexp.Regexp{},
		sessions:     map[string]*ProxySession{},
	}

	blockedPaths := []string{}
	blockedPaths = append(blockedPaths, config.BlockedPaths...)
	for _, blockedPath := range strings.Split(config.BlockedPathsStr, ",") {
		blockedPath = strings.Trim(blockedPath, " ")
		if blockedPath == "" {
			continue
		}
		blockedPaths = append(blockedPaths, blockedPath)
	}
	for _, blockedPath := range blockedPaths {
		blockedPathPattern, err := regexp.Compile(blockedPath)
		if err != nil {
			proxy.logger.Errorf("error parsing blocked path pattern '%v': %v", blockedPath, err)
			continue
		}
		proxy.blockedPaths = append(proxy.blockedPaths, *blockedPathPattern)
	}

	if config.CallTimeout == 0 {
		config.CallTimeout = 60 * time.Second
	}
	if config.SessionTimeout == 0 {
		config.SessionTimeout = 10 * time.Minute
	}

	if config.RebalanceInterval > 0 {
		go proxy.rebalanceSessionsLoop()
	}

	go proxy.cleanupSessions()
	return &proxy, nil
}

func (proxy *BeaconProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxy.processCall(w, r, pool.UnspecifiedClient)
}

func (proxy *BeaconProxy) ServeHealthCheckHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	canonicalFork := proxy.pool.GetCanonicalFork()
	if canonicalFork == nil || len(canonicalFork.ReadyClients) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("no_useable_endpoint"))
		return
	}

	w.Write([]byte("ready"))
}

func (proxy *BeaconProxy) processCall(w http.ResponseWriter, r *http.Request, clientType pool.ClientType) {
	if proxy.checkBlockedPaths(r.URL) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Path Blocked"))
		return
	}

	identifier, validAuth := proxy.CheckAuthorization(r)
	if !validAuth {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
		return
	}

	session := proxy.getSessionForRequest(r, identifier)
	if session.checkCallLimit(1) != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Call Limit exceeded"))
		return
	}

	var endpoint *pool.PoolClient
	if proxy.config.StickyEndpoint && proxy.pool.IsClientReady(session.lastPoolClient) {
		endpoint = session.lastPoolClient
	}

	nextEndpoint := r.Header.Get("X-Dugtrio-Next-Endpoint")
	if nextEndpoint == "" {
		nextEndpoint = r.URL.Query().Get("dugtrio-next-endpoint")
	}
	if nextEndpoint != "" {
		nextEndpointType := pool.ParseClientType(nextEndpoint)
		if nextEndpointType != pool.UnknownClient {
			clientType = nextEndpointType
		}
		endpoint = nil
	}
	if endpoint == nil || (clientType != pool.UnspecifiedClient && endpoint.GetClientType() != clientType) {
		endpoint = proxy.pool.GetReadyEndpoint(clientType)
		session.setLastPoolClient(endpoint)
	}
	if endpoint == nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("No Endpoint available"))
		return
	}

	session.requests.Add(1)

	err := proxy.processProxyCall(w, r, session, endpoint)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)

		proxy.logger.WithFields(logrus.Fields{
			"endpoint": endpoint.GetName(),
			"method":   r.Method,
			"url":      utils.GetRedactedUrl(r.URL.String()),
		}).Warnf("proxy error %v", err)
		w.Write([]byte("Internal Server Error"))
	}
}

func (proxy *BeaconProxy) checkBlockedPaths(url *url.URL) bool {
	for _, blockedPathPattern := range proxy.blockedPaths {
		match := blockedPathPattern.MatchString(url.EscapedPath())
		if match {
			return true
		}
	}
	return false
}

func (proxy *BeaconProxy) rebalanceSessionsLoop() {
	defer utils.HandleSubroutinePanic("proxy.session.rebalance")

	for {
		time.Sleep(proxy.config.RebalanceInterval)
		proxy.rebalanceSessions()
	}
}

func (proxy *BeaconProxy) rebalanceSessions() {
	canonicalFork := proxy.pool.GetCanonicalFork()
	if canonicalFork == nil || len(canonicalFork.ReadyClients) <= 1 {
		return
	}

	readyClients := canonicalFork.ReadyClients

	// Count sessions per endpoint
	endpointCounts := make(map[*pool.PoolClient]int)
	proxy.sessionMutex.Lock()
	totalSessions := 0
	for _, session := range proxy.sessions {
		if session.lastPoolClient != nil && slices.Contains(readyClients, session.lastPoolClient) {
			endpointCounts[session.lastPoolClient]++
			totalSessions++
		}
	}

	// Calculate ideal distribution
	idealCount := float64(totalSessions) / float64(len(readyClients))

	// Check if any endpoint exceeds threshold
	needsRebalance := false
	var diff float64
	for _, count := range endpointCounts {
		diff = math.Abs(float64(count)-idealCount) / idealCount
		if diff > proxy.config.RebalanceThreshold {
			needsRebalance = true
			break
		}
	}

	// Rebalance if needed
	if needsRebalance {
		proxy.logger.Infof("Rebalancing sessions (threshold exceeded: ideal=%v, diff=%v)", idealCount, diff)

		// Sort endpoints by session count
		type endpointCount struct {
			client *pool.PoolClient
			count  int
		}
		counts := make([]endpointCount, 0, len(endpointCounts))
		for client, count := range endpointCounts {
			counts = append(counts, endpointCount{client, count})
		}
		sort.Slice(counts, func(i, j int) bool {
			return counts[i].count > counts[j].count
		})

		rebalancedCount := 0
		// Redistribute sessions from most loaded to least loaded
		for _, session := range proxy.sessions {
			if session.lastPoolClient == counts[0].client {
				// Find least loaded endpoint
				for i := len(counts) - 1; i > 0; i-- {
					if slices.Contains(readyClients, counts[i].client) {
						session.setLastPoolClient(counts[i].client)
						endpointCounts[counts[0].client]--
						counts[0].count--
						endpointCounts[counts[i].client]++
						counts[i].count++
						rebalancedCount++
						break
					}
				}

				// Check if we've hit the rebalance limit
				if proxy.config.RebalanceMaxSweep > 0 && rebalancedCount >= proxy.config.RebalanceMaxSweep {
					proxy.logger.Infof("Rebalancing limit reached (%d sessions)", rebalancedCount)
					break
				}

				sort.Slice(counts, func(i, j int) bool {
					return counts[i].count > counts[j].count
				})
			}
		}
		proxy.logger.Infof("Rebalanced %d sessions", rebalancedCount)
	}
	proxy.sessionMutex.Unlock()
}
