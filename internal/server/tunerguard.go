package server

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// proxyHeaders are set by reverse proxies (nginx, Caddy, Traefik, Cloudflare Tunnel).
// Plex talks to the tuner directly, so a request carrying one of them came from outside.
var proxyHeaders = []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "CF-Connecting-IP"}

// tunerGuard limits the login-free tuner endpoints to the local network, so the
// service can be published behind a reverse proxy without exposing streams and guide.
type tunerGuard struct {
	all   bool           // FLIMMERKISTE_TUNER_ALLOW=all: no restriction
	extra []netip.Prefix // additional allowed networks from FLIMMERKISTE_TUNER_ALLOW
	log   *slog.Logger

	mu     sync.Mutex
	logged map[string]time.Time // last warning per client, keeps the log readable
}

// newTunerGuard parses FLIMMERKISTE_TUNER_ALLOW: "all", or IPs/CIDRs separated by commas or spaces.
func newTunerGuard(allow string, log *slog.Logger) *tunerGuard {
	g := &tunerGuard{log: log, logged: map[string]time.Time{}}
	for _, f := range strings.FieldsFunc(allow, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if strings.EqualFold(f, "all") {
			g.all = true
			continue
		}
		if p, err := netip.ParsePrefix(f); err == nil {
			g.extra = append(g.extra, p.Masked())
		} else if a, err := netip.ParseAddr(f); err == nil {
			a = a.Unmap()
			g.extra = append(g.extra, netip.PrefixFrom(a, a.BitLen()))
		} else {
			log.Warn("FLIMMERKISTE_TUNER_ALLOW: entry ignored", "entry", f)
		}
	}
	return g
}

// check returns an empty string if the request may use the tuner, otherwise the reason.
func (g *tunerGuard) check(r *http.Request) string {
	if g.all {
		return ""
	}
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return "via reverse proxy (" + h + ")"
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown client address"
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || isOwnAddr(addr) {
		return ""
	}
	for _, p := range g.extra {
		if p.Contains(addr) {
			return ""
		}
	}
	return "not a local network address"
}

// isOwnAddr reports whether addr belongs to this machine, e.g. Plex on the same
// server reaching the tuner via its public IP.
func isOwnAddr(addr netip.Addr) bool {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, ia := range ifaces {
		if n, ok := ia.(*net.IPNet); ok {
			if a, ok := netip.AddrFromSlice(n.IP); ok && a.Unmap() == addr {
				return true
			}
		}
	}
	return false
}

func (g *tunerGuard) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reason := g.check(r); reason != "" {
			g.warn(r, reason)
			http.Error(w, "tuner only available in the local network", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (g *tunerGuard) warn(r *http.Request, reason string) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := host + "|" + reason
	g.mu.Lock()
	last, seen := g.logged[key]
	now := time.Now()
	if seen && now.Sub(last) < 10*time.Minute {
		g.mu.Unlock()
		return
	}
	if len(g.logged) > 1000 {
		clear(g.logged)
	}
	g.logged[key] = now
	g.mu.Unlock()
	g.log.Warn("tuner request blocked (allow with FLIMMERKISTE_TUNER_ALLOW)", "client", host, "path", r.URL.Path, "reason", reason)
}
