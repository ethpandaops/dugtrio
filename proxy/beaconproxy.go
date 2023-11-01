package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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
	"Cookie",
	"Referer",
	"User-Agent",
}

var passthruResponseHeaderKeys = [...]string{
	"Content-Encoding",
	"Content-Language",
	"Content-Type",
	"Date",
	"Etag",
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
	for _, blockedPath := range config.BlockedPaths {
		blockedPaths = append(blockedPaths, blockedPath)
	}
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

	go proxy.cleanupSessions()
	return &proxy, nil
}

func (proxy *BeaconProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxy.processCall(w, r, pool.UnspecifiedClient)
}

func (proxy *BeaconProxy) processCall(w http.ResponseWriter, r *http.Request, clientType pool.ClientType) {
	if proxy.checkBlockedPaths(r.URL) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Path Blocked"))
		return
	}

	session := proxy.getSessionForRequest(r)
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
	if endpoint == nil {
		endpoint = proxy.pool.GetReadyEndpoint(clientType)
		session.lastPoolClient = endpoint
	}
	if endpoint == nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("No Endpoint available"))
		return
	}
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

func (proxy *BeaconProxy) processProxyCall(w http.ResponseWriter, r *http.Request, session *ProxySession, endpoint *pool.PoolClient) error {
	endpointConfig := endpoint.GetEndpointConfig()

	// get filtered headers
	hh := http.Header{}
	for _, hk := range passthruRequestHeaderKeys {
		if hv, ok := r.Header[hk]; ok {
			hh[hk] = hv
		}
	}
	for hk, hv := range endpointConfig.Headers {
		hh.Add(hk, hv)
	}

	proxyIpChain := []string{}
	if forwaredFor := r.Header.Get("X-Forwarded-For"); forwaredFor != "" {
		proxyIpChain = strings.Split(forwaredFor, ", ")
	}
	proxyIpChain = append(proxyIpChain, r.RemoteAddr)
	hh.Set("X-Forwarded-For", strings.Join(proxyIpChain, ", "))

	// build proxy url
	queryArgs := ""
	if r.URL.RawQuery != "" {
		queryArgs = fmt.Sprintf("?%s", r.URL.RawQuery)
	}
	proxyUrl, err := url.Parse(fmt.Sprintf("%s%s%s", endpointConfig.Url, r.URL.EscapedPath(), queryArgs))
	if err != nil {
		return fmt.Errorf("error parsing proxy url: %w", err)
	}

	// construct request to send to origin server
	rr := http.Request{
		Method:        r.Method,
		URL:           proxyUrl,
		Header:        hh,
		Body:          r.Body,
		ContentLength: r.ContentLength,
		Close:         r.Close,
	}
	start := time.Now()
	client := &http.Client{Timeout: proxy.config.CallTimeout}
	resp, err := client.Do(&rr)
	if err != nil {
		return fmt.Errorf("proxy request error: %w", err)
	}
	defer resp.Body.Close()

	// add to stats
	if proxy.proxyMetrics != nil {
		callDuration := time.Since(start)
		proxy.proxyMetrics.AddCall(endpoint.GetName(), fmt.Sprintf("%s%s", r.Method, r.URL.EscapedPath()), callDuration, resp.StatusCode)
	}

	// passthru response headers
	respH := w.Header()
	for _, hk := range passthruResponseHeaderKeys {
		if hv, ok := resp.Header[hk]; ok {
			respH[hk] = hv
		}
	}
	respH.Set("X-Dugtrio-Version", fmt.Sprintf("dugtrio/%v", utils.GetVersion()))
	respH.Set("X-Dugtrio-Session-Ip", session.GetIpAddr())
	respH.Set("X-Dugtrio-Session-Tokens", fmt.Sprintf("%.2f", session.getCallLimitTokens()))
	respH.Set("X-Dugtrio-Endpoint-Name", endpoint.GetName())
	respH.Set("X-Dugtrio-Endpoint-Type", endpoint.GetClientType().String())
	respH.Set("X-Dugtrio-Endpoint-Version", endpoint.GetVersion())
	w.WriteHeader(resp.StatusCode)

	// stream response body
	rspLen, err := io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("proxy stream error: %w", err)
	}

	proxy.logger.Debugf("proxied %v %v call (endpoint: %v, status: %v, length: %v)", r.Method, r.URL.EscapedPath(), endpoint.GetName(), resp.StatusCode, rspLen)
	return nil
}
