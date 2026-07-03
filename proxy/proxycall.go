package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/utils"
)

// errFailoverAttempt signals that the upstream call failed in a way that should be
// retried on the next failover candidate endpoint.
var errFailoverAttempt = errors.New("endpoint failover")

// failoverMaxBodySize is the maximum request body size that gets buffered for replay
// on alternate endpoints.
const failoverMaxBodySize = 1024 * 1024

// failoverContext tracks the state of an endpoint failover eligible call across attempts.
type failoverContext struct {
	pathClass   string
	body        []byte
	hasBody     bool
	candidates  []*pool.Client
	nextIdx     int
	attempts    int
	maxAttempts int
}

func (failoverCtx *failoverContext) hasNext() bool {
	if failoverCtx.maxAttempts >= 0 && failoverCtx.attempts >= failoverCtx.maxAttempts {
		return false
	}

	return failoverCtx.nextIdx < len(failoverCtx.candidates)
}

func (failoverCtx *failoverContext) nextEndpoint() *pool.Client {
	if !failoverCtx.hasNext() {
		return nil
	}

	endpoint := failoverCtx.candidates[failoverCtx.nextIdx]
	failoverCtx.nextIdx++
	failoverCtx.attempts++

	return endpoint
}

// isFailoverStatusCode returns true for response codes that indicate the endpoint cannot
// serve the requested data while another endpoint possibly can (eg. pruned historical states).
func isFailoverStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusNotFound, http.StatusInternalServerError, http.StatusNotImplemented, http.StatusServiceUnavailable:
		return true
	}

	return false
}

type proxyCallContext struct {
	context      context.Context
	cancelFn     context.CancelFunc
	cancelled    bool
	deadline     time.Time
	updateChan   chan time.Duration
	streamReader io.ReadCloser
}

func (proxy *BeaconProxy) newProxyCallContext(parent context.Context, timeout time.Duration) *proxyCallContext {
	callCtx := &proxyCallContext{
		deadline:   time.Now().Add(timeout),
		updateChan: make(chan time.Duration, 5),
	}
	callCtx.context, callCtx.cancelFn = context.WithCancel(parent)

	go callCtx.processCallContext()

	return callCtx
}

func (callContext *proxyCallContext) processCallContext() {
ctxLoop:
	for {
		timeout := time.Until(callContext.deadline)
		select {
		case newTimeout := <-callContext.updateChan:
			callContext.deadline = time.Now().Add(newTimeout)
		case <-callContext.context.Done():
			break ctxLoop
		case <-time.After(timeout):
			callContext.cancelFn()
			callContext.cancelled = true

			time.Sleep(10 * time.Millisecond)
		}
	}

	callContext.cancelled = true

	if callContext.streamReader != nil {
		callContext.streamReader.Close()
	}
}

// doProxyAttempt sends the call to the given endpoint and returns the response to stream back.
// It returns an errFailoverAttempt wrapped error when the attempt failed but should be retried
// on the next failover candidate endpoint.
func (proxy *BeaconProxy) doProxyAttempt(r *http.Request, callContext *proxyCallContext, endpoint *pool.Client, failoverCtx *failoverContext, proxyURL *url.URL, hh http.Header) (*http.Response, error) {
	// construct request to send to origin server
	reqBody := r.Body
	if failoverCtx != nil && failoverCtx.hasBody {
		reqBody = io.NopCloser(bytes.NewReader(failoverCtx.body))
	}

	req := &http.Request{
		Method:        r.Method,
		URL:           proxyURL,
		Header:        hh,
		Body:          reqBody,
		ContentLength: r.ContentLength,
		Close:         r.Close,
	}

	// while failover candidates remain, bound this attempt so a hanging endpoint doesn't
	// consume the whole call timeout (the last attempt runs on the remaining call timeout)
	reqContext := callContext.context

	var attemptTimer *time.Timer

	if failoverCtx != nil && failoverCtx.hasNext() {
		var attemptCancel context.CancelFunc

		// the cancel func is not deferred on purpose: on an accepted response the attempt
		// context must stay alive for body streaming, it gets cleaned up with callContext
		reqContext, attemptCancel = context.WithCancel(callContext.context)
		attemptTimer = time.AfterFunc(proxy.config.FailoverAttemptTimeout, attemptCancel)
	}

	start := time.Now()
	client := &http.Client{Timeout: 0}
	req = req.WithContext(reqContext)

	resp, err := client.Do(req)
	if err != nil {
		if failoverCtx != nil && failoverCtx.hasNext() && !callContext.cancelled && callContext.context.Err() == nil {
			return nil, fmt.Errorf("%w: request error on %v: %w", errFailoverAttempt, endpoint.GetName(), err)
		}

		return nil, fmt.Errorf("proxy request error: %w", err)
	}

	if callContext.cancelled {
		resp.Body.Close()
		return nil, fmt.Errorf("proxy context cancelled")
	}

	// add to stats
	if proxy.proxyMetrics != nil {
		callDuration := time.Since(start)
		proxy.proxyMetrics.AddCall(endpoint.GetName(), fmt.Sprintf("%s%s", r.Method, r.URL.EscapedPath()), callDuration, resp.StatusCode)
	}

	if failoverCtx != nil && isFailoverStatusCode(resp.StatusCode) && failoverCtx.hasNext() {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: status %v from %v", errFailoverAttempt, resp.StatusCode, endpoint.GetName())
	}

	if attemptTimer != nil && !attemptTimer.Stop() && reqContext.Err() != nil {
		// the attempt timeout fired before the response got accepted
		resp.Body.Close()

		if failoverCtx.hasNext() {
			return nil, fmt.Errorf("%w: attempt timeout on %v", errFailoverAttempt, endpoint.GetName())
		}

		return nil, fmt.Errorf("proxy attempt timeout")
	}

	return resp, nil
}

func (proxy *BeaconProxy) processProxyCall(w http.ResponseWriter, r *http.Request, callContext *proxyCallContext, session *Session, endpoint *pool.Client, failoverCtx *failoverContext) error {
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

	proxyIPChain := []string{}
	if forwaredFor := r.Header.Get("X-Forwarded-For"); forwaredFor != "" {
		proxyIPChain = strings.Split(forwaredFor, ", ")
	}

	proxyIPChain = append(proxyIPChain, r.RemoteAddr)
	hh.Set("X-Forwarded-For", strings.Join(proxyIPChain, ", "))

	// build proxy url
	queryArgs := ""
	if r.URL.RawQuery != "" {
		queryArgs = fmt.Sprintf("?%s", r.URL.RawQuery)
	}

	proxyURL, err := url.Parse(fmt.Sprintf("%s%s%s", endpointConfig.URL, r.URL.EscapedPath(), queryArgs))
	if err != nil {
		return fmt.Errorf("error parsing proxy url: %w", err)
	}

	resp, err := proxy.doProxyAttempt(r, callContext, endpoint, failoverCtx, proxyURL, hh) //nolint:bodyclose // body is closed via callContext.streamReader when the call context ends
	if err != nil {
		return err
	}

	callContext.streamReader = resp.Body

	respContentType := resp.Header.Get("Content-Type")
	isEventStream := respContentType == "text/event-stream" || strings.HasPrefix(r.URL.EscapedPath(), "/eth/v1/events")

	// passthru response headers
	respH := w.Header()

	for _, hk := range passthruResponseHeaderKeys {
		if hv, ok := resp.Header[hk]; ok {
			respH[hk] = hv
		}
	}

	respH.Set("X-Dugtrio-Version", fmt.Sprintf("dugtrio/%v", utils.GetVersion()))
	respH.Set("X-Dugtrio-Session-Ip", session.group.GetIPAddr())
	respH.Set("X-Dugtrio-Session-Tokens", fmt.Sprintf("%.2f", session.group.getCallLimitTokens()))
	respH.Set("X-Dugtrio-Endpoint-Name", endpoint.GetName())
	respH.Set("X-Dugtrio-Endpoint-Type", endpoint.GetClientType().String())
	respH.Set("X-Dugtrio-Endpoint-Version", endpoint.GetVersion())

	if failoverCtx != nil && failoverCtx.attempts > 0 {
		respH.Set("X-Dugtrio-Failover-Attempts", fmt.Sprintf("%d", failoverCtx.attempts))

		// remember this endpoint as the preferred failover target for this path class
		if resp.StatusCode < 400 {
			proxy.setFailoverEndpoint(failoverCtx.pathClass, endpoint)
		}
	}

	if isEventStream {
		respH.Set("X-Accel-Buffering", "no")
	}

	w.WriteHeader(resp.StatusCode)

	var respLen int64

	if isEventStream {
		callContext.updateChan <- proxy.config.CallTimeout

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		rspLen, err := proxy.processEventStreamResponse(callContext, w, resp.Body, session)
		if err != nil {
			proxy.logger.Warnf("proxy event stream error: %v", err)
		}

		respLen = rspLen
	} else {
		// stream response body
		rspLen, err := io.Copy(w, resp.Body)
		if err != nil {
			return fmt.Errorf("proxy response stream error: %w", err)
		}

		respLen = rspLen
	}

	proxy.logger.Debugf("proxied %v %v call (ip: %v, status: %v, length: %v, endpoint: %v)", r.Method, r.URL.EscapedPath(), session.group.GetIPAddr(), resp.StatusCode, respLen, endpoint.GetName())

	return nil
}

func (proxy *BeaconProxy) processEventStreamResponse(callContext *proxyCallContext, w http.ResponseWriter, r io.ReadCloser, session *Session) (int64, error) {
	rd := bufio.NewReaderSize(r, 64*1024)
	written := int64(0)

	for {
		for {
			evt, err := rd.ReadSlice('\n')
			if err != nil {
				return written, err
			}

			wb, err := w.Write(evt)
			if err != nil {
				return written, err
			}

			written += int64(wb)

			if wb == 1 || (wb == 2 && evt[0] == '\r') {
				break
			}
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if callContext.cancelled {
			return written, nil
		}

		now := time.Now()
		session.group.lastSeen = now
		session.lastSeen = now

		callContext.updateChan <- proxy.config.CallTimeout
	}
}
