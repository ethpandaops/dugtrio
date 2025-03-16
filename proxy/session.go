package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/utils"
	"golang.org/x/time/rate"
)

type ProxySession struct {
	ipAddr         string
	limiter        *rate.Limiter
	firstSeen      time.Time
	lastSeen       time.Time
	lastPoolClient *pool.PoolClient
	requests       atomic.Uint64
	activeContexts sync.Map
}

func (proxy *BeaconProxy) getSessionForRequest(r *http.Request, ident string) *ProxySession {
	var ip string

	if proxy.config.ProxyCount > 0 {
		forwardIps := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		forwardIdx := len(forwardIps) - int(proxy.config.ProxyCount)
		if forwardIdx >= 0 {
			ip = strings.Trim(forwardIps[forwardIdx], " ")
		}
	}
	if ip == "" {
		var err error
		ip, _, err = net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return nil
		}
	}

	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()

	if ident != "" {
		ip = fmt.Sprintf("%s-%s", ip, ident)
	}

	session := proxy.sessions[ip]
	if session == nil {
		session = &ProxySession{
			ipAddr:    ip,
			firstSeen: time.Now(),
			lastSeen:  time.Now(),
		}
		if proxy.config.CallRateLimit > 0 {
			session.limiter = rate.NewLimiter(rate.Limit(proxy.config.CallRateLimit), int(proxy.config.CallRateBurst))
		}
		proxy.sessions[ip] = session
	} else {
		session.lastSeen = time.Now()
	}
	return session
}

func (proxy *BeaconProxy) GetSessions() []*ProxySession {
	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()
	sessions := []*ProxySession{}
	for _, session := range proxy.sessions {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(a, b int) bool {
		return sessions[b].firstSeen.After(sessions[a].firstSeen)
	})
	return sessions
}

func (proxy *BeaconProxy) cleanupSessions() {
	defer utils.HandleSubroutinePanic("proxy.session.cleanup")

	for {
		time.Sleep(time.Minute)

		proxy.sessionMutex.Lock()
		for ip, session := range proxy.sessions {
			if time.Since(session.lastSeen) > proxy.config.SessionTimeout {
				delete(proxy.sessions, ip)
			}
		}
		proxy.sessionMutex.Unlock()
	}
}

func (session *ProxySession) checkCallLimit(callCost uint) error {
	if session.limiter == nil {
		return nil
	}
	if !session.limiter.AllowN(time.Now(), int(callCost)) {
		return fmt.Errorf("call rate limit exceeded")
	}
	return nil
}

func (session *ProxySession) getCallLimitTokens() float64 {
	if session.limiter == nil {
		return 0
	}
	return session.limiter.Tokens()
}

func (session *ProxySession) GetIpAddr() string {
	return session.ipAddr
}

func (session *ProxySession) GetFirstSeen() time.Time {
	return session.firstSeen
}

func (session *ProxySession) GetLastSeen() time.Time {
	return session.lastSeen
}

func (session *ProxySession) GetLastPoolClient() *pool.PoolClient {
	return session.lastPoolClient
}

func (session *ProxySession) GetRequests() uint64 {
	return session.requests.Load()
}

func (session *ProxySession) GetLimiterTokens() float64 {
	if session.limiter == nil {
		return 0
	}
	return session.limiter.Tokens()
}

func (session *ProxySession) updateLastSeen() {
	session.lastSeen = time.Now()
}

func (session *ProxySession) addActiveContext(cancel context.CancelFunc) {
	session.activeContexts.Store(cancel, struct{}{})
}

func (session *ProxySession) removeActiveContext(cancel context.CancelFunc) {
	session.activeContexts.Delete(cancel)
}

func (session *ProxySession) cancelActiveConnections() {
	session.activeContexts.Range(func(key, value interface{}) bool {
		cancel := key.(context.CancelFunc)
		cancel()
		session.activeContexts.Delete(key)
		return true
	})
}

func (session *ProxySession) setLastPoolClient(client *pool.PoolClient) {
	if session.lastPoolClient != client {
		session.cancelActiveConnections()
		session.lastPoolClient = client
	}
}
