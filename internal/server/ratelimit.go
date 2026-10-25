package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimit allows each client address a burst of requests that refills
// steadily. It guards the endpoints open before sign-in (login, PIN, setup),
// which reach out to plex.tv or a media server for every call.
type rateLimit struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

const (
	rateBurst  = 10
	ratePerMin = 10
)

func (l *rateLimit) allow(addr string, now time.Time) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	b := l.buckets[host]
	if b == nil {
		if len(l.buckets) > 10000 {
			l.prune(now)
		}
		b = &bucket{tokens: rateBurst, last: now}
		l.buckets[host] = b
	}
	b.tokens = min(rateBurst, b.tokens+now.Sub(b.last).Minutes()*ratePerMin)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// prune drops the buckets that are full again.
func (l *rateLimit) prune(now time.Time) {
	for host, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Minutes()*ratePerMin >= rateBurst {
			delete(l.buckets, host)
		}
	}
}

func (s *Server) limited(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.rate.allow(r.RemoteAddr, time.Now()) {
			w.Header().Set("Retry-After", "60")
			s.fail(w, r, http.StatusTooManyRequests, "api.tooManyRequests")
			return
		}
		h(w, r)
	}
}
