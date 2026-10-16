package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTunerGuard(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name   string
		allow  string
		remote string
		header string
		ok     bool
	}{
		{"loopback", "", "127.0.0.1:5000", "", true},
		{"lan", "", "192.168.1.20:5000", "", true},
		{"docker bridge", "", "172.17.0.2:5000", "", true},
		{"ipv6 loopback", "", "[::1]:5000", "", true},
		{"ipv4-mapped lan", "", "[::ffff:10.0.0.5]:5000", "", true},
		{"public", "", "203.0.113.7:5000", "", false},
		{"proxy on same host", "", "127.0.0.1:5000", "X-Forwarded-For", false},
		{"cloudflare tunnel", "", "127.0.0.1:5000", "CF-Connecting-IP", false},
		{"allowed ip", "203.0.113.7", "203.0.113.7:5000", "", true},
		{"allowed cidr", "10.1.0.0/16, 100.64.0.0/10", "100.100.1.2:5000", "", true},
		{"allowed ip, still behind proxy", "203.0.113.7", "203.0.113.7:5000", "X-Real-IP", false},
		{"all", "all", "203.0.113.7:5000", "X-Forwarded-For", true},
		{"invalid entry ignored", "nonsense", "203.0.113.7:5000", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newTunerGuard(c.allow, log)
			r := httptest.NewRequest(http.MethodGet, "/lineup.json", nil)
			r.RemoteAddr = c.remote
			if c.header != "" {
				r.Header.Set(c.header, "198.51.100.1")
			}
			w := httptest.NewRecorder()
			g.wrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })(w, r)
			if got := w.Code == http.StatusOK; got != c.ok {
				t.Fatalf("allowed = %v, want %v (status %d)", got, c.ok, w.Code)
			}
		})
	}
}
