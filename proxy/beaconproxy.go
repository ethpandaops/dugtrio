package proxy

import (
	"context"
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

// responseWriterTracker wraps http.ResponseWriter to record whether any
// response has been committed. Used to send a 503 if no upstream succeeded.
type responseWriterTracker struct {
	http.ResponseWriter
	wrote bool
}

func (rw *responseWriterTracker) WriteHeader(status int) {
	rw.wrote = true
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriterTracker) Write(b []byte) (int, error) {
	rw.wrote = true
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriterTracker) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type BeaconProxy struct {
	config       *types.ProxyConfig
	pool         *pool.BeaconPool
	proxyMetrics *metrics.ProxyMetrics
	logger       *logrus.Entry
	blockedPaths []*regexp.Regexp

	sessionMutex sync.Mutex
	sessions     map[string]*Session
}

func NewBeaconProxy(config *types.ProxyConfig, beaconPool *pool.BeaconPool, proxyMetrics *metrics.ProxyMetrics) (*BeaconProxy, error) {
	proxy := BeaconProxy{
		config:       config,
		pool:         beaconPool,
		proxyMetrics: proxyMetrics,
		logger:       logrus.WithField("module", "proxy"),
		blockedPaths: []*regexp.Regexp{},
		sessions:     map[string]*Session{},
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

func (proxy *BeaconProxy) processCall(w http.ResponseWriter, r *http.Request, clientType pool.ClientType) {
	rw := &responseWriterTracker{ResponseWriter: w}

	if proxy.checkBlockedPaths(r.URL) {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusForbidden)

		if _, err := rw.Write([]byte("Path Blocked")); err != nil {
			proxy.logger.Warnf("error writing path blocked response: %v", err)
		}

		return
	}

	identifier, validAuth := proxy.CheckAuthorization(r)
	if !validAuth {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusUnauthorized)

		if _, err := rw.Write([]byte("Unauthorized")); err != nil {
			proxy.logger.Warnf("error writing unauthorized response: %v", err)
		}

		return
	}

	session := proxy.getSessionForRequest(r, identifier)
	if session.checkCallLimit(1) != nil {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusTooManyRequests)

		if _, err := rw.Write([]byte("Call Limit exceeded")); err != nil {
			proxy.logger.Warnf("error writing call limit exceeded response: %v", err)
		}

		return
	}

	var body []byte

	if r.Body != nil {
		var err error

		body, err = io.ReadAll(r.Body)
		if err != nil {
			rw.Header().Set("Content-Type", "text/plain")
			rw.WriteHeader(http.StatusInternalServerError)
			proxy.logger.Warnf("error reading request body: %v", err)

			return
		}
	}

	// Total budget covers the full retry chain: one callTimeout per endpoint.
	// Using a single callTimeout for callCtx means the fallback has zero budget
	// after the primary exhausts it (e.g. hanging during beacon restart).
	totalTimeout := proxy.config.CallTimeout * time.Duration(len(proxy.pool.GetAllEndpoints())+1)
	callCtx := proxy.newProxyCallContext(r.Context(), totalTimeout)
	contextID := session.addActiveContext(callCtx.cancelFn)

	// Guard: if we exit without writing any response (e.g. all attempts timed out
	// and callCtx was cancelled before writeProxyResponse committed headers),
	// return 503 explicitly so the client never receives an empty 200.
	defer func() {
		if !rw.wrote {
			proxy.logger.WithFields(logrus.Fields{
				"method": utils.SanitizeLogParam(r.Method),
				"url":    utils.SanitizeLogParam(utils.GetRedactedURL(r.URL.String())),
			}).Warn("no response written to client — sending 503")
			rw.ResponseWriter.Header().Set("Content-Type", "text/plain")
			rw.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)

			if _, wErr := rw.ResponseWriter.Write([]byte("upstream timeout")); wErr != nil {
				proxy.logger.Warnf("error writing upstream timeout response: %v", wErr)
			}
		}
	}()

	defer func() {
		callCtx.cancelFn()
		session.removeActiveContext(contextID)
	}()

	var tried []string

	for {
		endpoint := proxy.pool.GetReadyEndpointExcluding(clientType, tried)
		if endpoint == nil {
			rw.Header().Set("Content-Type", "text/plain")
			rw.WriteHeader(http.StatusServiceUnavailable)

			if _, err := rw.Write([]byte("no upstream available")); err != nil {
				proxy.logger.Warnf("error writing no upstream response: %v", err)
			}

			return
		}

		tried = append(tried, endpoint.GetName())

		// Each attempt gets its own fresh timeout so a slow/hanging primary
		// does not consume the fallback's time budget.
		// attemptCancel must be called after the response body is fully streamed,
		// not before — canceling early drops the HTTP connection mid-body.
		attemptCtx, attemptCancel := context.WithTimeout(r.Context(), proxy.config.CallTimeout)

		resp, err := proxy.doUpstreamRequest(attemptCtx, r, body, endpoint)
		if err != nil {
			attemptCancel()

			proxy.logger.WithFields(logrus.Fields{
				"endpoint": endpoint.GetName(),
				"method":   utils.SanitizeLogParam(r.Method),
				"url":      utils.SanitizeLogParam(utils.GetRedactedURL(r.URL.String())),
			}).Warnf("upstream request failed, trying next: %v", err)

			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			attemptCancel()

			proxy.logger.WithFields(logrus.Fields{
				"endpoint": endpoint.GetName(),
				"method":   utils.SanitizeLogParam(r.Method),
				"url":      utils.SanitizeLogParam(utils.GetRedactedURL(r.URL.String())),
				"status":   resp.StatusCode,
			}).Warnf("upstream returned non-2xx, trying next")

			continue
		}

		// Detect empty body regardless of Content-Length header value.
		// ContentLength==0 covers explicit "Content-Length: 0".
		// ContentLength==-1 covers chunked/unknown — we must peek to detect
		// truly empty bodies (e.g. beacon mid-restart returning chunked 200 with no data).
		peek := make([]byte, 1)
		n, peekErr := resp.Body.Read(peek)

		if n == 0 {
			resp.Body.Close()
			attemptCancel()

			proxy.logger.WithFields(logrus.Fields{
				"endpoint":       endpoint.GetName(),
				"method":         utils.SanitizeLogParam(r.Method),
				"url":            utils.SanitizeLogParam(utils.GetRedactedURL(r.URL.String())),
				"content_length": resp.ContentLength,
				"peek_error":     peekErr,
			}).Warnf("upstream returned empty body on 2xx, trying next")

			continue
		}

		// Reconstruct the body stream: prepend the peeked byte.
		resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(string(peek[:n])), resp.Body))

		// peekErr may be io.EOF if the entire response was exactly 1 byte — valid
		// data. Log any unexpected non-EOF error for diagnostics but proceed since
		// we have n==1 bytes of real data.
		if peekErr != nil && peekErr != io.EOF {
			proxy.logger.WithFields(logrus.Fields{
				"endpoint": endpoint.GetName(),
				"method":   utils.SanitizeLogParam(r.Method),
				"url":      utils.SanitizeLogParam(utils.GetRedactedURL(r.URL.String())),
			}).Debugf("unexpected peek error (proceeding with n=1 byte): %v", peekErr)
		}

		session.requests.Add(1)

		if _, err = proxy.writeProxyResponse(rw, r, session, resp, endpoint, callCtx); err != nil {
			proxy.logger.WithFields(logrus.Fields{
				"endpoint": endpoint.GetName(),
			}).Warnf("proxy stream error: %v", err)
		}

		attemptCancel()

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

	// Count sessions per endpoint
	endpointCounts := make(map[*pool.Client]int)
	for _, client := range readyClients {
		endpointCounts[client] = 0
	}

	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()

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
	diff := 0.0
	absDiff := 0

	needsRebalance := func() bool {
		for _, count := range endpointCounts {
			diff = math.Abs(float64(count)-idealCount) / idealCount
			absDiff = int(math.Abs(float64(count) - idealCount))

			// Use the minimum of percentage and absolute thresholds
			if diff > proxy.config.RebalanceThreshold && absDiff > proxy.config.RebalanceAbsThreshold {
				return true
			}
		}

		return false
	}

	// Rebalance if needed
	if needsRebalance() {
		proxy.logger.Infof("Rebalancing sessions (threshold exceeded: ideal=%.2f, diff=%.2f%%, abs_diff=%v)", idealCount, diff*100, absDiff)

		rebalancedCount := 0
		rebalanceOne := func() bool {
			// Sort endpoints by session count
			type endpointCount struct {
				client *pool.Client
				count  int
			}

			counts := make([]endpointCount, 0, len(endpointCounts))

			for client, count := range endpointCounts {
				counts = append(counts, endpointCount{client, count})
			}

			if len(counts) <= 1 {
				return false
			}

			sort.Slice(counts, func(i, j int) bool {
				return counts[i].count > counts[j].count
			})

			var targetClient *pool.Client

			var targetCountsIndex int

			for i := len(counts) - 1; i > 0; i-- {
				if slices.Contains(readyClients, counts[i].client) {
					targetClient = counts[i].client
					targetCountsIndex = i

					break
				}
			}

			if targetClient == nil || targetClient == counts[0].client {
				return false
			}

			sessions := make([]*Session, 0, counts[0].count)
			for _, session := range proxy.sessions {
				if session.lastPoolClient == counts[0].client {
					sessions = append(sessions, session)
				}
			}

			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].lastRebalance.Before(sessions[j].lastRebalance)
			})

			if len(sessions) == 0 {
				return false
			}

			session := sessions[0]

			session.setLastPoolClient(targetClient)

			endpointCounts[counts[0].client]--
			counts[0].count--
			endpointCounts[targetClient]++
			counts[targetCountsIndex].count++
			rebalancedCount++

			proxy.logger.Infof("Rebalanced session %v: %v -> %v", session.GetIPAddr(), counts[0].client.GetName(), targetClient.GetName())

			return true
		}

		for rebalanceOne() {
			if !needsRebalance() {
				break
			}

			if proxy.config.RebalanceMaxSweep > 0 && rebalancedCount >= proxy.config.RebalanceMaxSweep {
				break
			}
		}

		proxy.logger.Infof("Rebalanced %d sessions (threshold exceeded: ideal=%.2f, diff=%.2f%%, abs_diff=%v)", rebalancedCount, idealCount, diff*100, absDiff)
	}
}
