package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/store"
)

// fakeJellyfin knows an administrator and a viewer.
func fakeJellyfin(t *testing.T) *httptest.Server {
	t.Helper()
	keys := []map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"Id": "srv1", "ServerName": "Wohnzimmer", "Version": "12.2.0", "ProductName": "Jellyfin Server"})
	})
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Pw string }
		json.NewDecoder(r.Body).Decode(&in)
		users := map[string][3]string{"admin": {"u-admin", "secret", "1"}, "gast": {"u-gast", "gast", ""}}
		u, ok := users[in.Username]
		if !ok || u[1] != in.Pw {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"AccessToken": "session-" + u[0], "ServerId": "srv1",
			"User": map[string]any{"Id": u[0], "Name": in.Username, "Policy": map[string]bool{"IsAdministrator": u[2] == "1"}}})
	})
	mux.HandleFunc("GET /Auth/Keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"Items": keys})
	})
	mux.HandleFunc("POST /Auth/Keys", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), `Token="session-u-admin"`) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		keys = append(keys, map[string]string{"AccessToken": "api-key-1", "AppName": r.URL.Query().Get("app")})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /Sessions/Logout", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestJellyfinLogin(t *testing.T) {
	jf := fakeJellyfin(t)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(config.Config{DataDir: dir, Horizon: 6}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	login := func(body map[string]string) (int, string, []*http.Cookie) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(b))
		req.Header.Set("Accept-Language", "de-DE,de;q=0.9")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String(), rec.Result().Cookies()
	}
	ctx := context.Background()

	var info map[string]any
	req := httptest.NewRequest("POST", "/api/setup/connect", strings.NewReader(`{"url":"`+jf.URL+`/"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &info)
	if info["platform"] != "jellyfin" || info["name"] != "Wohnzimmer" {
		t.Fatalf("connect: %s", rec.Body.String())
	}

	// only an administrator may claim the instance
	if code, body, _ := login(map[string]string{"url": jf.URL, "username": "gast", "password": "gast"}); code != http.StatusForbidden || !strings.Contains(body, "Administrator") {
		t.Errorf("viewer claimed: %d %s", code, body)
	}
	if code, _, _ := login(map[string]string{"url": jf.URL, "username": "admin", "password": "falsch"}); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", code)
	}
	code, body, cookies := login(map[string]string{"url": jf.URL, "username": "admin", "password": "secret"})
	if code != http.StatusOK || len(cookies) == 0 {
		t.Fatalf("admin: %d %s", code, body)
	}
	for k, want := range map[string]string{store.SetMediaServer: "jellyfin", store.SetServerToken: "api-key-1", store.SetOwnerID: "u-admin",
		store.SetServerUserID: "u-admin", store.SetLanguage: "de", store.SetServerName: "Wohnzimmer"} {
		if got := st.Setting(ctx, k, ""); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	u, _ := st.SessionUser(ctx, cookies[0].Value)
	if u == nil || u.Role != "admin" || u.Provider != "jellyfin" {
		t.Errorf("owner: %+v", u)
	}

	// later: the viewer signs in as viewer, unless shared users are off
	if code, body, _ := login(map[string]string{"username": "gast", "password": "gast"}); code != http.StatusOK || !strings.Contains(body, `"role":"viewer"`) {
		t.Errorf("viewer: %d %s", code, body)
	}
	st.SetSetting(ctx, store.SetAllowSharedUser, "0")
	if code, _, _ := login(map[string]string{"username": "gast", "password": "gast"}); code != http.StatusForbidden {
		t.Errorf("viewer with sharing off: %d", code)
	}
	if code, body, _ := login(map[string]string{"username": "admin", "password": "secret"}); code != http.StatusOK || !strings.Contains(body, `"role":"admin"`) {
		t.Errorf("owner again: %d %s", code, body)
	}
}

// A Jellyfin or Emby instance takes no plex.tv sign-in.
func TestNoPlexSignInOnJellyfin(t *testing.T) {
	srv, h := transferWorld(t, "a")
	srv.store.SetSetting(context.Background(), store.SetMediaServer, "jellyfin")
	srv.pins[42] = time.Now()
	for _, r := range []struct{ method, path string }{{"POST", "/api/auth/pin"}, {"GET", "/api/auth/pin/42"}} {
		if code := call(t, h, r.method, r.path, nil, nil); code != http.StatusBadRequest {
			t.Errorf("%s %s: %d", r.method, r.path, code)
		}
	}
}
