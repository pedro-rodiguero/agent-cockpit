package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter counts attempts per key in a sliding window, in memory. Every attempt counts, right or wrong.
// It is safe for concurrent use.
type Limiter struct {
	Now  func() time.Time
	mu   sync.Mutex
	hits map[string][]time.Time
}

// Take records an attempt for key and reports whether it fits in limit per window.
func (l *Limiter) Take(key string, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	if len(l.hits) > 20_000 {
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= window {
				delete(l.hits, k)
			}
		}
	}
	recent := l.hits[key][:0:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// TrustProxy says which proxy, if any, sits in front of the server.
type TrustProxy string

const (
	// ProxyNone: the connection's address is the client's.
	ProxyNone TrustProxy = "none"
	// ProxyCaddy: Caddy on the same machine, which appends the address it saw to X-Forwarded-For.
	// Earlier entries were written by the client and are ignored.
	ProxyCaddy TrustProxy = "caddy"
)

// ClientIP is the client's address, decided in one place for the rate limits and the audit log. A
// proxy header counts only when the connection comes from loopback, where the trusted proxy runs.
func ClientIP(r *http.Request, mode TrustProxy) string {
	peer := cleanIP(r.RemoteAddr)
	if mode != ProxyCaddy || !isLoopback(peer) {
		if peer == "" {
			return "unknown"
		}
		return peer
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	if ip := cleanIP(strings.TrimSpace(hops[len(hops)-1])); ip != "" {
		return ip
	}
	return peer
}

func cleanIP(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}

func isLoopback(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}
