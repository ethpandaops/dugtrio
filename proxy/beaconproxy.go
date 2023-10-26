package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

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
	config *types.ProxyConfig
	pool   *pool.BeaconPool
	logger *logrus.Entry
}

func NewBeaconProxy(config *types.ProxyConfig, pool *pool.BeaconPool) (*BeaconProxy, error) {
	proxy := BeaconProxy{
		config: config,
		pool:   pool,
		logger: logrus.WithField("module", "proxy"),
	}
	return &proxy, nil
}

func (proxy *BeaconProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	// TODO: serve proxy call
	endpoint := proxy.pool.GetReadyEndpoint()
	err := proxy.processProxyCall(w, r, endpoint)

	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)

		proxy.logger.WithFields(logrus.Fields{
			"endpoint": endpoint.GetName(),
			"method":   r.Method,
			"url":      utils.GetRedactedUrl(r.URL.String()),
		}).Warnf("proxy error %v", err)
	}
}

func (proxy *BeaconProxy) processProxyCall(w http.ResponseWriter, r *http.Request, endpoint *pool.PoolClient) error {
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

	client := &http.Client{Timeout: time.Second * 300}
	resp, err := client.Do(&rr)
	if err != nil {
		return fmt.Errorf("proxy request error: %w", err)
	}

	defer resp.Body.Close()

	// passthru response headers
	respH := w.Header()
	for _, hk := range passthruResponseHeaderKeys {
		if hv, ok := resp.Header[hk]; ok {
			respH[hk] = hv
		}
	}
	w.WriteHeader(resp.StatusCode)

	// stream response body
	rspLen, err := io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("proxy stream error: %w", err)
	}

	proxy.logger.Debugf("proxied %v %v call (endpoint: %v, status: %v, length: %v)", r.Method, r.URL.EscapedPath(), endpoint.GetName(), resp.StatusCode, rspLen)
	return nil
}
