package proxy

import (
	"errors"
	"fmt"
	"io"
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

// defaultFailoverPaths are the api paths eligible for endpoint failover by default.
// These are data queries where availability differs per endpoint (eg. historical states
// are only served by nodes with state archiving enabled).
var defaultFailoverPaths = []string{
	"^/eth/v[0-9]+/beacon/states/",
	"^/eth/v[0-9]+/debug/beacon/states/",
}

const (
	defaultFailoverMaxAttempts    = 8
	defaultFailoverAttemptTimeout = 20 * time.Second
)

type BeaconProxy struct {
	config       *types.ProxyConfig
	pool         *pool.BeaconPool
	proxyMetrics *metrics.ProxyMetrics
	logger       *logrus.Entry
	blockedPaths []*regexp.Regexp

	sessionMutex sync.Mutex
	sessions     map[string]*SessionGroup

	failoverPaths     []*regexp.Regexp
	failoverMutex     sync.Mutex
	failoverEndpoints map[string]string
}

func NewBeaconProxy(config *types.ProxyConfig, beaconPool *pool.BeaconPool, proxyMetrics *metrics.ProxyMetrics) (*BeaconProxy, error) {
	proxy := BeaconProxy{
		config:            config,
		pool:              beaconPool,
		proxyMetrics:      proxyMetrics,
		logger:            logrus.WithField("module", "proxy"),
		blockedPaths:      []*regexp.Regexp{},
		sessions:          make(map[string]*SessionGroup),
		failoverPaths:     []*regexp.Regexp{},
		failoverEndpoints: map[string]string{},
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

		proxy.blockedPaths = append(proxy.blockedPaths, blockedPathPattern)
	}

	if !config.FailoverDisabled {
		failoverPaths := config.FailoverPaths
		if failoverPaths == nil {
			failoverPaths = defaultFailoverPaths
		}

		for _, failoverPath := range failoverPaths {
			failoverPathPattern, err := regexp.Compile(failoverPath)
			if err != nil {
				proxy.logger.Errorf("error parsing failover path pattern '%v': %v", failoverPath, err)
				continue
			}

			proxy.failoverPaths = append(proxy.failoverPaths, failoverPathPattern)
		}
	}

	if config.CallTimeout == 0 {
		config.CallTimeout = 60 * time.Second
	}

	if config.SessionTimeout == 0 {
		config.SessionTimeout = 10 * time.Minute
	}

	if config.FailoverMaxAttempts == 0 {
		config.FailoverMaxAttempts = defaultFailoverMaxAttempts
	}

	if config.FailoverAttemptTimeout == 0 {
		config.FailoverAttemptTimeout = defaultFailoverAttemptTimeout
	}

	if config.RebalanceInterval > 0 {
		go proxy.rebalanceSessionsLoop()
	}

	go proxy.cleanupSessions()

	return &proxy, nil
}

func (proxy *BeaconProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxy.processCall(w, r, pool.UnspecifiedClient, pool.UnspecifiedClient)
}

func (proxy *BeaconProxy) ServeHealthCheckHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	canonicalFork := proxy.pool.GetCanonicalFork()
	if canonicalFork == nil || len(canonicalFork.ReadyClients) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)

		_, err := w.Write([]byte("no_useable_endpoint"))
		if err != nil {
			proxy.logger.Warnf("error writing no useable endpoint response: %v", err)
		}

		return
	}

	_, err := w.Write([]byte("ready"))
	if err != nil {
		proxy.logger.Warnf("error writing ready response: %v", err)
	}
}

func (proxy *BeaconProxy) processCall(w http.ResponseWriter, r *http.Request, clientType, sessionPrefix pool.ClientType) {
	if proxy.checkBlockedPaths(r.URL) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)

		_, err := w.Write([]byte("Path Blocked"))
		if err != nil {
			proxy.logger.Warnf("error writing path blocked response: %v", err)
		}

		return
	}

	identifier, validAuth := proxy.CheckAuthorization(r)
	if !validAuth {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)

		_, err := w.Write([]byte("Unauthorized"))
		if err != nil {
			proxy.logger.Warnf("error writing unauthorized response: %v", err)
		}

		return
	}

	session := proxy.getSessionForRequest(r, identifier, sessionPrefix)
	if session.group.checkCallLimit(1) != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTooManyRequests)

		_, err := w.Write([]byte("Call Limit exceeded"))
		if err != nil {
			proxy.logger.Warnf("error writing call limit exceeded response: %v", err)
		}

		return
	}

	endpoint, effectiveClientType, pinned, err := proxy.getEndpointForCall(r, session, clientType)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)

		_, err := w.Write([]byte(err.Error()))
		if err != nil {
			proxy.logger.Warnf("error writing no endpoint available response: %v", err)
		}

		return
	}

	if endpoint == nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)

		_, err := w.Write([]byte("No Endpoint available"))
		if err != nil {
			proxy.logger.Warnf("error writing no endpoint available response: %v", err)
		}

		return
	}

	session.group.requests.Add(1)

	failoverCtx := proxy.newFailoverContext(r, endpoint, effectiveClientType, pinned)

	callContext := proxy.newProxyCallContext(r.Context(), proxy.config.CallTimeout)
	contextID := session.addActiveContext(callContext.cancelFn)

	defer func() {
		callContext.cancelFn()
		session.removeActiveContext(contextID)
	}()

	for {
		err = proxy.processProxyCall(w, r, callContext, session, endpoint, failoverCtx)
		if err == nil {
			return
		}

		if failoverCtx != nil && errors.Is(err, errFailoverAttempt) {
			nextEndpoint := failoverCtx.nextEndpoint()
			if nextEndpoint != nil {
				proxy.logger.WithFields(logrus.Fields{
					"method": r.Method,
					"url":    utils.GetRedactedURL(r.URL.String()),
				}).Debugf("failover: retrying on %v (%v)", nextEndpoint.GetName(), err)

				endpoint = nextEndpoint

				continue
			}
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)

		proxy.logger.WithFields(logrus.Fields{
			"endpoint": endpoint.GetName(),
			"method":   r.Method,
			"url":      utils.GetRedactedURL(r.URL.String()),
		}).Warnf("proxy error %v", err)

		_, err = w.Write([]byte("Internal Server Error"))
		if err != nil {
			proxy.logger.Warnf("error writing internal server error response: %v", err)
		}

		return
	}
}

func (proxy *BeaconProxy) checkBlockedPaths(reqURL *url.URL) bool {
	for _, blockedPathPattern := range proxy.blockedPaths {
		match := blockedPathPattern.MatchString(reqURL.EscapedPath())
		if match {
			return true
		}
	}

	return false
}

func getMinCgcForPath(path string) uint16 {
	if strings.HasPrefix(path, "/eth/v1/beacon/blobs/") {
		return 64 // 64 is the minimum CGC for blobs
	}

	return 0
}

func (proxy *BeaconProxy) getEndpointForCall(r *http.Request, session *Session, clientType pool.ClientType) (endpoint *pool.Client, effectiveClientType pool.ClientType, pinned bool, err error) {
	if proxy.config.StickyEndpoint && proxy.pool.IsClientReady(session.lastPoolClient) {
		endpoint = session.lastPoolClient
	}

	minCgc := getMinCgcForPath(r.URL.Path)

	nextEndpoint := r.Header.Get("X-Dugtrio-Next-Endpoint")
	if nextEndpoint == "" {
		nextEndpoint = r.URL.Query().Get("dugtrio-next-endpoint")
	}

	if nextEndpoint != "" {
		endpoint = nil

		nextEndpointType := pool.ParseClientType(nextEndpoint)
		if nextEndpointType != pool.UnknownClient {
			clientType = nextEndpointType
		} else if client := proxy.pool.GetEndpointByName(nextEndpoint); client != nil {
			if client.GetCustodyGroupCount() < minCgc {
				return nil, clientType, false, fmt.Errorf("endpoint %s has too low CGC (%d < %d)", nextEndpoint, client.GetCustodyGroupCount(), minCgc)
			}

			endpoint = client
			clientType = pool.UnspecifiedClient
			pinned = true
		} else {
			return nil, clientType, false, fmt.Errorf("no endpoint matches X-Dugtrio-Next-Endpoint filter")
		}
	}

	if minCgc > 0 && endpoint != nil {
		if endpoint.GetCustodyGroupCount() < minCgc {
			endpoint = nil
			pinned = false
		}
	}

	if endpoint == nil || (clientType != pool.UnspecifiedClient && endpoint.GetClientType() != clientType) {
		endpoint = proxy.pool.GetReadyEndpoint(clientType, minCgc)
		pinned = false

		if minCgc == 0 {
			session.setLastPoolClient(endpoint)
		}
	}

	return endpoint, clientType, pinned, nil
}

// newFailoverContext prepares an endpoint failover context for calls matching the configured
// failover paths. Returns nil if the call is not eligible for failover (unmatched path,
// explicitly pinned endpoint, non-replayable body or no alternate endpoints).
func (proxy *BeaconProxy) newFailoverContext(r *http.Request, firstEndpoint *pool.Client, clientType pool.ClientType, pinned bool) *failoverContext {
	if pinned || len(proxy.failoverPaths) == 0 {
		return nil
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		return nil
	}

	pathClass := ""
	reqPath := r.URL.EscapedPath()

	for _, failoverPathPattern := range proxy.failoverPaths {
		if failoverPathPattern.MatchString(reqPath) {
			pathClass = failoverPathPattern.String()
			break
		}
	}

	if pathClass == "" {
		return nil
	}

	failoverCtx := &failoverContext{
		pathClass:   pathClass,
		maxAttempts: proxy.config.FailoverMaxAttempts,
	}

	// buffer the request body so it can be replayed on alternate endpoints
	if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
		if r.ContentLength < 0 || r.ContentLength > failoverMaxBodySize {
			return nil
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, failoverMaxBodySize))
		if err != nil {
			return nil
		}

		failoverCtx.body = body
		failoverCtx.hasBody = true
	}

	failoverCtx.candidates = proxy.getFailoverCandidates(pathClass, firstEndpoint, clientType, getMinCgcForPath(r.URL.Path))
	if len(failoverCtx.candidates) == 0 {
		return nil
	}

	return failoverCtx
}

// getFailoverCandidates returns the ready endpoints to try as failover alternatives, ordered by
// the last endpoint known to serve this path class first, followed by the remaining endpoints
// interleaved by client type so each client implementation is covered as early as possible.
func (proxy *BeaconProxy) getFailoverCandidates(pathClass string, firstEndpoint *pool.Client, clientType pool.ClientType, minCgc uint16) []*pool.Client {
	canonicalFork := proxy.pool.GetCanonicalFork()
	if canonicalFork == nil {
		return nil
	}

	preferredName := proxy.getFailoverEndpointName(pathClass)

	var preferred *pool.Client

	clientsByType := map[pool.ClientType][]*pool.Client{}
	clientTypes := []pool.ClientType{}

	for _, client := range canonicalFork.ReadyClients {
		if client == firstEndpoint {
			continue
		}

		if clientType != pool.UnspecifiedClient && client.GetClientType() != clientType {
			continue
		}

		if minCgc > 0 && client.GetCustodyGroupCount() < minCgc {
			continue
		}

		if preferred == nil && client.GetName() == preferredName {
			preferred = client
			continue
		}

		cType := client.GetClientType()
		if _, ok := clientsByType[cType]; !ok {
			clientTypes = append(clientTypes, cType)
		}

		clientsByType[cType] = append(clientsByType[cType], client)
	}

	candidates := make([]*pool.Client, 0, len(canonicalFork.ReadyClients))
	if preferred != nil {
		candidates = append(candidates, preferred)
	}

	for idx := 0; ; idx++ {
		added := false

		for _, cType := range clientTypes {
			if idx < len(clientsByType[cType]) {
				candidates = append(candidates, clientsByType[cType][idx])
				added = true
			}
		}

		if !added {
			break
		}
	}

	return candidates
}

func (proxy *BeaconProxy) getFailoverEndpointName(pathClass string) string {
	proxy.failoverMutex.Lock()
	defer proxy.failoverMutex.Unlock()

	return proxy.failoverEndpoints[pathClass]
}

func (proxy *BeaconProxy) setFailoverEndpoint(pathClass string, endpoint *pool.Client) {
	proxy.failoverMutex.Lock()
	defer proxy.failoverMutex.Unlock()

	proxy.failoverEndpoints[pathClass] = endpoint.GetName()
}

func (proxy *BeaconProxy) rebalanceSessionsLoop() {
	defer utils.HandleSubroutinePanic("proxy.session.rebalance", proxy.rebalanceSessionsLoop)

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

	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()

	// Collect all sessions across all groups.
	allSessions := make([]*Session, 0, len(proxy.sessions)*2)

	for _, group := range proxy.sessions {
		group.sessionMutex.Lock()

		for _, session := range group.sessions {
			allSessions = append(allSessions, session)
		}

		group.sessionMutex.Unlock()
	}

	// Count total sessions per endpoint across all prefixes.
	// This gives us the real load each endpoint is handling.
	endpointTotals := make(map[*pool.Client]int, len(readyClients))
	for _, client := range readyClients {
		endpointTotals[client] = 0
	}

	totalSessions := 0

	for _, session := range allSessions {
		if session.lastPoolClient != nil && slices.Contains(readyClients, session.lastPoolClient) {
			endpointTotals[session.lastPoolClient]++
			totalSessions++
		}
	}

	if totalSessions == 0 {
		return
	}

	idealTotal := float64(totalSessions) / float64(len(readyClients))

	diff := 0.0
	absDiff := 0

	needsRebalance := func() bool {
		for _, count := range endpointTotals {
			diff = math.Abs(float64(count)-idealTotal) / idealTotal
			absDiff = int(math.Abs(float64(count) - idealTotal))

			if diff > proxy.config.RebalanceThreshold && absDiff > proxy.config.RebalanceAbsThreshold {
				return true
			}
		}

		return false
	}

	if !needsRebalance() {
		return
	}

	proxy.logger.Infof("Rebalancing sessions (ideal=%.2f, diff=%.2f%%, abs_diff=%v)", idealTotal, diff*100, absDiff)

	rebalancedCount := 0

	// Each iteration moves one session from the heaviest overloaded endpoint.
	// Unconstrained sessions are preferred because they can target any endpoint.
	// Constrained sessions can only move to endpoints of the same client type.
	rebalanceOne := func() bool {
		// Sort endpoints by total load, descending.
		type epLoad struct {
			client *pool.Client
			total  int
		}

		loads := make([]epLoad, 0, len(endpointTotals))
		for client, total := range endpointTotals {
			loads = append(loads, epLoad{client, total})
		}

		sort.Slice(loads, func(i, j int) bool {
			return loads[i].total > loads[j].total
		})

		// Try each overloaded endpoint, starting from the heaviest.
		for srcIdx := range loads {
			srcClient := loads[srcIdx].client
			srcTotal := loads[srcIdx].total

			if float64(srcTotal) <= idealTotal {
				break
			}

			// Partition movable sessions on this endpoint by constraint type.
			var unconstrained []*Session

			constrained := make(map[pool.ClientType][]*Session, 2)

			for _, session := range allSessions {
				if session.lastPoolClient != srcClient {
					continue
				}

				if session.prefix == pool.UnspecifiedClient {
					unconstrained = append(unconstrained, session)
				} else {
					constrained[session.prefix] = append(constrained[session.prefix], session)
				}
			}

			// Try unconstrained first — can target any underloaded endpoint.
			if len(unconstrained) > 0 {
				// Find most underloaded endpoint (last in sorted list).
				var target *pool.Client

				for tIdx := len(loads) - 1; tIdx > srcIdx; tIdx-- {
					if loads[tIdx].total < srcTotal {
						target = loads[tIdx].client

						break
					}
				}

				if target != nil {
					sort.Slice(unconstrained, func(i, j int) bool {
						return unconstrained[i].lastRebalance.Before(unconstrained[j].lastRebalance)
					})

					session := unconstrained[0]
					session.setLastPoolClient(target)

					endpointTotals[srcClient]--
					endpointTotals[target]++
					rebalancedCount++

					proxy.logger.Infof("Rebalanced session %v [main]: %v -> %v",
						session.group.GetIPAddr(),
						srcClient.GetName(), target.GetName())

					return true
				}
			}

			// Try constrained — can only target underloaded endpoints of same type.
			for clientType, candidates := range constrained {
				var target *pool.Client

				for tIdx := len(loads) - 1; tIdx > srcIdx; tIdx-- {
					if loads[tIdx].client.GetClientType() == clientType && loads[tIdx].total < srcTotal {
						target = loads[tIdx].client

						break
					}
				}

				if target == nil {
					continue
				}

				sort.Slice(candidates, func(i, j int) bool {
					return candidates[i].lastRebalance.Before(candidates[j].lastRebalance)
				})

				session := candidates[0]
				session.setLastPoolClient(target)

				endpointTotals[srcClient]--
				endpointTotals[target]++
				rebalancedCount++

				proxy.logger.Infof("Rebalanced session %v [%v]: %v -> %v",
					session.group.GetIPAddr(), session.prefix,
					srcClient.GetName(), target.GetName())

				return true
			}
		}

		return false
	}

	for rebalanceOne() {
		if !needsRebalance() {
			break
		}

		if proxy.config.RebalanceMaxSweep > 0 && rebalancedCount >= proxy.config.RebalanceMaxSweep {
			break
		}
	}

	proxy.logger.Infof("Rebalanced %d sessions", rebalancedCount)
}
