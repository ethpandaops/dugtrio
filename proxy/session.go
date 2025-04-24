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

type Session struct {
	ipAddr         string
	limiter        *rate.Limiter
	firstSeen      time.Time
	lastSeen       time.Time
	lastPoolClient *pool.Client
	lastRebalance  time.Time
	requests       atomic.Uint64
	activeContexts struct {
		sync.Mutex
		contexts map[uint64]context.CancelFunc
		nextID   uint64
	}
}

func (session *Session) init() {
	session.activeContexts.contexts = make(map[uint64]context.CancelFunc)
}

func (proxy *BeaconProxy) getSessionForRequest(r *http.Request, ident string) *Session {
	var ip string

	if proxy.config.ProxyCount > 0 {
		forwardIps := strings.Split(r.Header.Get("X-Forwarded-For"), ",")

		forwardIdx := len(forwardIps) - proxy.config.ProxyCount
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
		session = &Session{
			ipAddr:        ip,
			firstSeen:     time.Now(),
			lastSeen:      time.Now(),
			lastRebalance: time.Now(),
		}
		session.init()

		if proxy.config.CallRateLimit > 0 {
			session.limiter = rate.NewLimiter(rate.Limit(proxy.config.CallRateLimit), proxy.config.CallRateBurst)
		}

		proxy.sessions[ip] = session
	} else {
		session.lastSeen = time.Now()
	}

	return session
}

func (proxy *BeaconProxy) GetSessions() []*Session {
	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()

	sessions := []*Session{}
	for _, session := range proxy.sessions {
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(a, b int) bool {
		return sessions[b].firstSeen.After(sessions[a].firstSeen)
	})

	return sessions
}

func (proxy *BeaconProxy) cleanupSessions() {
	defer utils.HandleSubroutinePanic("proxy.session.cleanup", proxy.cleanupSessions)

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

func (session *Session) checkCallLimit(callCost int) error {
	if session.limiter == nil {
		return nil
	}

	if !session.limiter.AllowN(time.Now(), callCost) {
		return fmt.Errorf("call rate limit exceeded")
	}

	return nil
}

func (session *Session) getCallLimitTokens() float64 {
	if session.limiter == nil {
		return 0
	}

	return session.limiter.Tokens()
}

func (session *Session) GetIPAddr() string {
	return session.ipAddr
}

func (session *Session) GetFirstSeen() time.Time {
	return session.firstSeen
}

func (session *Session) GetLastSeen() time.Time {
	return session.lastSeen
}

func (session *Session) GetLastPoolClient() *pool.Client {
	return session.lastPoolClient
}

func (session *Session) GetRequests() uint64 {
	return session.requests.Load()
}

func (session *Session) GetLimiterTokens() float64 {
	if session.limiter == nil {
		return 0
	}

	return session.limiter.Tokens()
}

func (session *Session) updateLastSeen() {
	session.lastSeen = time.Now()
}

func (session *Session) addActiveContext(cancel context.CancelFunc) uint64 {
	session.activeContexts.Lock()
	defer session.activeContexts.Unlock()

	id := session.activeContexts.nextID
	session.activeContexts.nextID++
	session.activeContexts.contexts[id] = cancel

	return id
}

func (session *Session) removeActiveContext(id uint64) {
	session.activeContexts.Lock()
	defer session.activeContexts.Unlock()
	delete(session.activeContexts.contexts, id)
}

func (session *Session) cancelActiveConnections() {
	session.activeContexts.Lock()
	defer session.activeContexts.Unlock()

	for id, cancel := range session.activeContexts.contexts {
		cancel()
		delete(session.activeContexts.contexts, id)
	}
}

func (session *Session) setLastPoolClient(client *pool.Client) {
	if session.lastPoolClient != client {
		session.cancelActiveConnections()
		session.lastPoolClient = client
	}
}
