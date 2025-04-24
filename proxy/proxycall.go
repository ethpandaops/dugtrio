package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/utils"
)

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

func (proxy *BeaconProxy) processProxyCall(w http.ResponseWriter, r *http.Request, session *Session, endpoint *pool.Client) error {
	callContext := proxy.newProxyCallContext(r.Context(), proxy.config.CallTimeout)
	contextID := session.addActiveContext(callContext.cancelFn)

	defer func() {
		callContext.cancelFn()
		session.removeActiveContext(contextID)
	}()

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

	reqBody := r.Body

	if r.Method == "POST" {
		// analyze request body for validator stats
		switch r.URL.EscapedPath() {
		case "/eth/v1/validator/prepare_beacon_proposer":
			reqBody, err = proxy.analyzePrepareBeaconProposer(session, r.Body)
			if err != nil {
				return fmt.Errorf("error analyzing prepare beacon proposer: %w", err)
			}
		case "/eth/v1/validator/beacon_committee_subscriptions":
			reqBody, err = proxy.analyzeBeaconCommitteeSubscriptions(session, r.Body)
			if err != nil {
				return fmt.Errorf("error analyzing beacon committee subscriptions: %w", err)
			}
		}
	}

	// construct request to send to origin server
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
	req = req.WithContext(callContext.context)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("proxy request error: %w", err)
	}

	if callContext.cancelled {
		resp.Body.Close()
		return fmt.Errorf("proxy context cancelled")
	}

	callContext.streamReader = resp.Body

	// add to stats
	if proxy.proxyMetrics != nil {
		callDuration := time.Since(start)
		proxy.proxyMetrics.AddCall(endpoint.GetName(), fmt.Sprintf("%s%s", r.Method, r.URL.EscapedPath()), callDuration, resp.StatusCode)
	}

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
	respH.Set("X-Dugtrio-Session-Ip", session.GetIPAddr())
	respH.Set("X-Dugtrio-Session-Tokens", fmt.Sprintf("%.2f", session.getCallLimitTokens()))
	respH.Set("X-Dugtrio-Endpoint-Name", endpoint.GetName())
	respH.Set("X-Dugtrio-Endpoint-Type", endpoint.GetClientType().String())
	respH.Set("X-Dugtrio-Endpoint-Version", endpoint.GetVersion())

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

	proxy.logger.Debugf("proxied %v %v call (ip: %v, status: %v, length: %v, endpoint: %v)", r.Method, r.URL.EscapedPath(), session.GetIPAddr(), resp.StatusCode, respLen, endpoint.GetName())

	return nil
}

func (proxy *BeaconProxy) processEventStreamResponse(callContext *proxyCallContext, w http.ResponseWriter, r io.ReadCloser, session *Session) (int64, error) {
	rd := bufio.NewReader(r)
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

			if wb == 1 {
				break
			}
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if callContext.cancelled {
			return written, nil
		}

		session.updateLastSeen()
		callContext.updateChan <- proxy.config.CallTimeout
	}
}
