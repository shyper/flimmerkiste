package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// Pages of the web UI answer 200, every other path 404 (the page shows "page not found").
func TestUnknownPagesAre404(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	web := fstest.MapFS{"index.html": {Data: []byte("<html>")}, "assets/app.js": {Data: []byte("js")}}
	srv, err := New(config.Config{DataDir: dir}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), web)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	for path, want := range map[string]int{
		"/":                                     http.StatusOK,
		"/guide":                                http.StatusOK,
		"/settings":                             http.StatusOK,
		"/settings/mcp":                         http.StatusOK,
		"/channels/new":                         http.StatusOK,
		"/channels/new/smart":                   http.StatusOK,
		"/channels/12":                          http.StatusOK,
		"/setup":                                http.StatusOK,
		"/assets/app.js":                        http.StatusOK,
		"/assets/":                              http.StatusNotFound, // no directory listing
		"/nonsense":                             http.StatusNotFound,
		"/guide/extra":                          http.StatusNotFound,
		"/settings/mcp/extra":                   http.StatusNotFound,
		"/channels/12/x":                        http.StatusNotFound,
		"/api/nonsense":                         http.StatusNotFound,
		"/.well-known/oauth-protected-resource": http.StatusNotFound,
		"/.well-known/oauth-protected-resource/mcp": http.StatusNotFound,
		"/.well-known/oauth-authorization-server":   http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}

func TestTokensRedacted(t *testing.T) {
	token := "fk_" + randomHex(32)
	got := mediaserver.Redact("POST /mcp/" + token + " failed")
	if got != "POST /mcp/fk_*** failed" {
		t.Errorf("redacted: %q", got)
	}
}
