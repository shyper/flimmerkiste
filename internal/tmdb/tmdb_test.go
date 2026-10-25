package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A rate limited request is tried again; errors never show the API key.
func TestRetryAndKeyless(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := &Client{Key: "0123456789abcdef0123456789abcdef", Base: srv.URL}
	var out struct{ OK bool }
	if err := c.get(context.Background(), "/x", nil, &out); err != nil || !out.OK || calls != 2 {
		t.Fatalf("err %v, ok %v, calls %d", err, out.OK, calls)
	}
	c.Base = "http://bad host"
	if err := c.get(context.Background(), "/x", nil, &out); err == nil || strings.Contains(err.Error(), c.Key) {
		t.Fatalf("error shows the key or is missing: %v", err)
	}
}
