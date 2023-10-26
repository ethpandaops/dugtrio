package proxy

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ethpandaops/dugtrio/utils"
	"golang.org/x/time/rate"
)

type ProxySession struct {
	ipAddr    string
	limiter   *rate.Limiter
	firstSeen time.Time
	lastSeen  time.Time
}

func (proxy *BeaconProxy) getSessionForRequest(r *http.Request) *ProxySession {
	var ip string

	if proxy.Config.ProxyCount > 0 {
		forwardIps := strings.Split(r.Header.Get("X-Forwarded-For"), ", ")
		forwardIdx := len(forwardIps) - int(proxy.Config.ProxyCount)
		if forwardIdx >= 0 {
			ip = forwardIps[forwardIdx]
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

	session := proxy.sessions[ip]
	if session == nil {
		session = &ProxySession{
			ipAddr:    ip,
			firstSeen: time.Now(),
			lastSeen:  time.Now(),
		}
		if proxy.Config.CallRateLimit > 0 {
			session.limiter = rate.NewLimiter(rate.Limit(proxy.Config.CallRateLimit), int(proxy.Config.CallRateBurst))
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
			if time.Since(session.lastSeen) > proxy.Config.SessionTimeout {
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

func (session *ProxySession) GetIpAddr() string {
	return session.ipAddr
}

func (session *ProxySession) GetFirstSeen() time.Time {
	return session.firstSeen
}

func (session *ProxySession) GetLastSeen() time.Time {
	return session.lastSeen
}
