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

// SessionGroup holds shared state for all sessions from the same IP/ident.
// The rate limiter and request counter are shared across all prefix-specific
// sessions within the group, so rate limits apply per-client regardless of
// which prefix endpoints they use.
type SessionGroup struct {
	ipAddr    string
	limiter   *rate.Limiter
	firstSeen time.Time
	lastSeen  time.Time
	requests  atomic.Uint64

	sessionMutex sync.Mutex
	sessions     map[pool.ClientType]*Session
}

// Session holds per-prefix state for sticky endpoint selection and active
// connections. Each client-specific prefix (e.g. /lighthouse/, /prysm/) and
// the main endpoint get their own Session so their sticky endpoint choices
// don't interfere with each other.
type Session struct {
	group          *SessionGroup
	prefix         pool.ClientType
	lastPoolClient *pool.Client
	lastRebalance  time.Time
	activeContexts struct {
		sync.Mutex
		contexts map[uint64]context.CancelFunc
		nextID   uint64
	}
}

func (session *Session) init() {
	session.activeContexts.contexts = make(map[uint64]context.CancelFunc)
}

func (proxy *BeaconProxy) getSessionForRequest(r *http.Request, ident string, prefix pool.ClientType) *Session {
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

	group := proxy.sessions[ip]
	if group == nil {
		group = &SessionGroup{
			ipAddr:    ip,
			firstSeen: time.Now(),
			lastSeen:  time.Now(),
			sessions:  make(map[pool.ClientType]*Session, 4),
		}

		if proxy.config.CallRateLimit > 0 {
			group.limiter = rate.NewLimiter(rate.Limit(proxy.config.CallRateLimit), proxy.config.CallRateBurst)
		}

		proxy.sessions[ip] = group
	} else {
		group.lastSeen = time.Now()
	}

	group.sessionMutex.Lock()
	defer group.sessionMutex.Unlock()

	session := group.sessions[prefix]
	if session == nil {
		session = &Session{
			group:         group,
			prefix:        prefix,
			lastRebalance: time.Now(),
		}
		session.init()

		group.sessions[prefix] = session
	}

	return session
}

// GetSessionGroups returns all session groups sorted by firstSeen.
func (proxy *BeaconProxy) GetSessionGroups() []*SessionGroup {
	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()

	groups := make([]*SessionGroup, 0, len(proxy.sessions))
	for _, group := range proxy.sessions {
		groups = append(groups, group)
	}

	sort.Slice(groups, func(a, b int) bool {
		return groups[b].firstSeen.After(groups[a].firstSeen)
	})

	return groups
}

// GetAllSessions returns all prefix-specific sessions across all groups.
func (proxy *BeaconProxy) GetAllSessions() []*Session {
	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()

	sessions := make([]*Session, 0, len(proxy.sessions)*2)
	for _, group := range proxy.sessions {
		group.sessionMutex.Lock()
		for _, session := range group.sessions {
			sessions = append(sessions, session)
		}
		group.sessionMutex.Unlock()
	}

	sort.Slice(sessions, func(a, b int) bool {
		return sessions[b].group.firstSeen.After(sessions[a].group.firstSeen)
	})

	return sessions
}

func (proxy *BeaconProxy) cleanupSessions() {
	defer utils.HandleSubroutinePanic("proxy.session.cleanup", proxy.cleanupSessions)

	for {
		time.Sleep(time.Minute)

		proxy.sessionMutex.Lock()

		for ip, group := range proxy.sessions {
			if time.Since(group.lastSeen) > proxy.config.SessionTimeout {
				delete(proxy.sessions, ip)
			}
		}

		proxy.sessionMutex.Unlock()
	}
}

// SessionGroup methods

func (group *SessionGroup) checkCallLimit(callCost int) error {
	if group.limiter == nil {
		return nil
	}

	if !group.limiter.AllowN(time.Now(), callCost) {
		return fmt.Errorf("call rate limit exceeded")
	}

	return nil
}

func (group *SessionGroup) getCallLimitTokens() float64 {
	if group.limiter == nil {
		return 0
	}

	return group.limiter.Tokens()
}

func (group *SessionGroup) GetIPAddr() string {
	return group.ipAddr
}

func (group *SessionGroup) GetFirstSeen() time.Time {
	return group.firstSeen
}

func (group *SessionGroup) GetLastSeen() time.Time {
	return group.lastSeen
}

func (group *SessionGroup) GetRequests() uint64 {
	return group.requests.Load()
}

func (group *SessionGroup) GetLimiterTokens() float64 {
	if group.limiter == nil {
		return 0
	}

	return group.limiter.Tokens()
}

// GetSessions returns all prefix sessions within this group.
func (group *SessionGroup) GetSessions() []*Session {
	group.sessionMutex.Lock()
	defer group.sessionMutex.Unlock()

	sessions := make([]*Session, 0, len(group.sessions))
	for _, session := range group.sessions {
		sessions = append(sessions, session)
	}

	return sessions
}

// Session methods

func (session *Session) GetGroup() *SessionGroup {
	return session.group
}

func (session *Session) GetPrefix() pool.ClientType {
	return session.prefix
}

func (session *Session) GetLastPoolClient() *pool.Client {
	return session.lastPoolClient
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
