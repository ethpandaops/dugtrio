package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/utils"
)

// failoverMaxBodySize is the maximum request body size that gets buffered for replay
// on alternate endpoints.
const failoverMaxBodySize = 1024 * 1024

// failoverContext tracks the state of an endpoint failover eligible call across attempts.
type failoverContext struct {
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
	cancelled    atomic.Bool
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
			callContext.cancelled.Store(true)

			time.Sleep(10 * time.Millisecond)
		}
	}

	callContext.cancelled.Store(true)

	if callContext.streamReader != nil {
		callContext.streamReader.Close()
	}
}

// doProxyAttempt sends the call to the given endpoint and returns the upstream response.
func (proxy *BeaconProxy) doProxyAttempt(r *http.Request, reqContext context.Context, callContext *proxyCallContext, endpoint *pool.Client, failoverCtx *failoverContext) (*http.Response, error) {
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
		return nil, fmt.Errorf("error parsing proxy url: %w", err)
	}

	// construct request to send to origin server
	reqBody := r.Body
	if failoverCtx != nil {
		// attempts may run in parallel, so the original request body cannot be shared
		reqBody = http.NoBody
		if failoverCtx.hasBody {
			reqBody = io.NopCloser(bytes.NewReader(failoverCtx.body))
		}
	}

	req := &http.Request{
		Method:        r.Method,
		URL:           proxyURL,
		Header:        hh,
		Body:          reqBody,
		ContentLength: r.ContentLength,
		Close:         r.Close,
	}

	start := time.Now()
	client := &http.Client{Timeout: 0}
	req = req.WithContext(reqContext)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxy request error: %w", err)
	}

	if callContext.cancelled.Load() {
		resp.Body.Close()
		return nil, fmt.Errorf("proxy context cancelled")
	}

	// add to stats
	if proxy.proxyMetrics != nil {
		callDuration := time.Since(start)
		proxy.proxyMetrics.AddCall(endpoint.GetName(), fmt.Sprintf("%s%s", r.Method, r.URL.EscapedPath()), callDuration, resp.StatusCode)
	}

	return resp, nil
}

func (proxy *BeaconProxy) callLogEntry(r *http.Request) *logrus.Entry {
	return proxy.logger.WithFields(logrus.Fields{
		"method": r.Method,
		"url":    utils.GetRedactedURL(r.URL.String()),
	})
}

// proxyAttemptResult is the outcome of a single upstream attempt.
type proxyAttemptResult struct {
	endpoint *pool.Client
	resp     *http.Response
	err      error
}

// runProxyAttempts executes the upstream call, hedging across the failover candidates:
// fast upstream failures (failover eligible status codes and connection errors) advance to
// the next candidate immediately, while an attempt that stays silent for failoverHedgeDelay
// is left running and the next candidate is raced in parallel (bounded by
// failoverMaxParallel), so slow endpoints keep their full time to respond while hanging
// endpoints only cost latency. The first acceptable response wins; if all candidates fail,
// the last upstream failure response is returned so it can be passed through to the client.
func (proxy *BeaconProxy) runProxyAttempts(r *http.Request, callContext *proxyCallContext, firstEndpoint *pool.Client, failoverCtx *failoverContext) (*http.Response, *pool.Client, error) {
	if failoverCtx == nil {
		resp, err := proxy.doProxyAttempt(r, callContext.context, callContext, firstEndpoint, nil)
		return resp, firstEndpoint, err
	}

	results := make(chan *proxyAttemptResult, len(failoverCtx.candidates)+1)
	attemptCancels := map[*pool.Client]context.CancelFunc{}
	inflight := 0

	launchAttempt := func(endpoint *pool.Client) {
		// the cancel funcs are not deferred on purpose: the winning attempt's context must
		// stay alive for body streaming, it gets cleaned up with callContext
		attemptContext, attemptCancel := context.WithCancel(callContext.context)
		attemptCancels[endpoint] = attemptCancel
		inflight++

		go func() {
			resp, err := proxy.doProxyAttempt(r, attemptContext, callContext, endpoint, failoverCtx) //nolint:bodyclose // the receiver of the results channel owns and closes the body
			results <- &proxyAttemptResult{endpoint: endpoint, resp: resp, err: err}
		}()
	}

	// abandonAttempts cancels the still running attempts and closes their responses once they return
	abandonAttempts := func() {
		for _, attemptCancel := range attemptCancels {
			attemptCancel()
		}

		if inflight > 0 {
			go func(pending int) {
				for i := 0; i < pending; i++ {
					res := <-results
					if res.resp != nil {
						res.resp.Body.Close()
					}
				}
			}(inflight)
		}
	}

	launchAttempt(firstEndpoint)

	hedgeTimer := time.NewTimer(proxy.config.FailoverHedgeDelay)
	defer hedgeTimer.Stop()

	var fallback *proxyAttemptResult

	var lastErr error

	for {
		select {
		case res := <-results:
			inflight--

			delete(attemptCancels, res.endpoint)

			if res.err == nil && !isFailoverStatusCode(res.resp.StatusCode) {
				abandonAttempts()

				if fallback != nil {
					fallback.resp.Body.Close()
				}

				return res.resp, res.endpoint, nil
			}

			if res.err != nil {
				if callContext.cancelled.Load() || callContext.context.Err() != nil {
					abandonAttempts()

					if fallback != nil {
						fallback.resp.Body.Close()
					}

					return nil, res.endpoint, res.err
				}

				lastErr = res.err
			} else {
				// the endpoint cannot serve the requested data; keep the latest failure
				// response as fallback in case no other endpoint can serve it either
				if fallback != nil {
					fallback.resp.Body.Close()
				}

				fallback = res
			}

			if failoverCtx.hasNext() && inflight < proxy.config.FailoverMaxParallel {
				nextEndpoint := failoverCtx.nextEndpoint()
				proxy.callLogEntry(r).Debugf("failover: retrying on %v (%v failed)", nextEndpoint.GetName(), res.endpoint.GetName())
				launchAttempt(nextEndpoint)
				hedgeTimer.Reset(proxy.config.FailoverHedgeDelay)
			} else if inflight == 0 {
				// all candidates exhausted; pass the last upstream failure through
				if fallback != nil {
					return fallback.resp, fallback.endpoint, nil
				}

				return nil, res.endpoint, lastErr
			}

		case <-hedgeTimer.C:
			if failoverCtx.hasNext() && inflight < proxy.config.FailoverMaxParallel {
				nextEndpoint := failoverCtx.nextEndpoint()
				proxy.callLogEntry(r).Debugf("failover: hedging on %v after %v of silence", nextEndpoint.GetName(), proxy.config.FailoverHedgeDelay)
				launchAttempt(nextEndpoint)
			}

			hedgeTimer.Reset(proxy.config.FailoverHedgeDelay)

		case <-callContext.context.Done():
			abandonAttempts()

			if fallback != nil {
				fallback.resp.Body.Close()
			}

			return nil, firstEndpoint, fmt.Errorf("proxy context cancelled")
		}
	}
}

func (proxy *BeaconProxy) processProxyCall(w http.ResponseWriter, r *http.Request, callContext *proxyCallContext, session *Session, endpoint *pool.Client, failoverCtx *failoverContext) error {
	resp, endpoint, err := proxy.runProxyAttempts(r, callContext, endpoint, failoverCtx) //nolint:bodyclose // body is closed via callContext.streamReader when the call context ends
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

		if callContext.cancelled.Load() {
			return written, nil
		}

		now := time.Now()
		session.group.lastSeen = now
		session.lastSeen = now

		callContext.updateChan <- proxy.config.CallTimeout
	}
}
