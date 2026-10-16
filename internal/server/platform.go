package server

// The media server platform (Plex, Jellyfin or Emby) and the sign-in through
// Jellyfin and Emby: there is no central account like plex.tv, so people sign
// in with name and password on the linked server. The first sign-in links the
// server; only one of its administrators can do that, and Flimmerkiste keeps an
// API key of its own instead of the password.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/jellyfin"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// platform is "plex", "jellyfin" or "emby".
func (s *Server) platform(ctx context.Context) string {
	switch v := s.store.Setting(ctx, store.SetMediaServer, "plex"); v {
	case "jellyfin", "emby":
		return v
	}
	return "plex"
}

// platformName is the product name shown to people.
func platformName(p string) string {
	switch p {
	case "jellyfin":
		return "Jellyfin"
	case "emby":
		return "Emby"
	}
	return "Plex"
}

// jellyfinClient returns the client of a linked Jellyfin or Emby server.
func (s *Server) jellyfinClient(ctx context.Context) (*jellyfin.Client, error) {
	url := s.store.Setting(ctx, store.SetServerURL, "")
	token := s.store.Setting(ctx, store.SetServerToken, "")
	if url == "" || token == "" {
		return nil, library.ErrNotLinked
	}
	return &jellyfin.Client{Flavor: jellyfin.Flavor(s.platform(ctx)), BaseURL: url, Token: token,
		DeviceID: s.instanceID(ctx), UserID: s.store.Setting(ctx, store.SetServerUserID, "")}, nil
}

// handleSetupConnect checks the address of a Jellyfin or Emby server before
// the first sign-in.
func (s *Server) handleSetupConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.store.Setting(ctx, store.SetOwnerID, "") != "" {
		s.fail(w, r, http.StatusForbidden, "api.adminOnly")
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.URL) == "" {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	url := normalizeURL(in.URL)
	info, flavor, err := jellyfin.Public(ctx, url)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "auth.unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url, "platform": flavor, "name": info.ServerName, "version": info.Version,
		"product": platformName(string(flavor))})
}

func normalizeURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	return u
}

// handleLogin signs in with name and password on Jellyfin or Emby. Before the
// instance is claimed it also links the server (url given).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Username) == "" {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	claimed := s.store.Setting(ctx, store.SetOwnerID, "") != ""
	var url string
	var flavor jellyfin.Flavor
	if claimed {
		if s.platform(ctx) == "plex" {
			s.fail(w, r, http.StatusBadRequest, "api.badRequest")
			return
		}
		url, flavor = s.store.Setting(ctx, store.SetServerURL, ""), jellyfin.Flavor(s.platform(ctx))
	} else {
		url = normalizeURL(in.URL)
		var err error
		if _, flavor, err = jellyfin.Public(ctx, url); err != nil {
			s.fail(w, r, http.StatusBadGateway, "auth.unreachable")
			return
		}
	}
	sess, err := jellyfin.Login(ctx, flavor, url, s.instanceID(ctx), strings.TrimSpace(in.Username), in.Password)
	var se *jellyfin.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden) {
		s.fail(w, r, http.StatusUnauthorized, "auth.wrongPassword")
		return
	} else if err != nil {
		s.fail(w, r, http.StatusBadGateway, "auth.unreachable")
		return
	}
	session := &jellyfin.Client{Flavor: flavor, BaseURL: url, Token: sess.Token, DeviceID: s.instanceID(ctx), UserID: sess.UserID}
	// The password's session is not needed afterwards.
	defer func() {
		lctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Logout(lctx)
	}()

	u := &store.User{Provider: string(flavor), ExternalID: sess.UserID, Username: sess.UserName, Role: "viewer"}
	owner := s.store.Setting(ctx, store.SetOwnerID, "")
	switch {
	case !claimed:
		if !sess.Admin {
			s.fail(w, r, http.StatusForbidden, "auth.notAdmin", platformName(string(flavor)))
			return
		}
		key, err := session.CreateAPIKey(ctx, "Flimmerkiste")
		if err != nil {
			s.log.Warn("create API key", "err", mediaserver.Redact(err.Error()))
			s.fail(w, r, http.StatusBadGateway, "auth.apiKey")
			return
		}
		info, _, _ := jellyfin.Public(ctx, url)
		name := ""
		if info != nil {
			name = info.ServerName
		}
		if err := s.store.SetSettings(ctx, map[string]string{
			store.SetMediaServer: string(flavor), store.SetServerURL: url, store.SetServerToken: key,
			store.SetServerID: sess.ServerID, store.SetServerName: name, store.SetServerUserID: sess.UserID,
			store.SetOwnerID: sess.UserID, store.SetLanguage: string(s.reqLang(r)),
		}); err != nil {
			s.failErr(w, r, http.StatusInternalServerError, err)
			return
		}
		u.Role = "admin"
		s.lib.StartSync()
	case owner == sess.UserID:
		u.Role = "admin"
	default:
		if s.store.Setting(ctx, store.SetAllowSharedUser, "1") != "1" {
			s.fail(w, r, http.StatusForbidden, "auth.deniedServer")
			return
		}
	}
	if err := s.store.UpsertUser(ctx, u); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if u.Role == "admin" && owner == sess.UserID {
		_ = s.store.SetUserRole(ctx, u.ID, "admin")
	}
	if err := s.startSession(w, r, u); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": u})
}

// liveTVBase is the address the media server reaches the tuner at.
func (s *Server) liveTVBase(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return strings.TrimRight(s.cfg.BaseURL, "/")
	}
	return s.baseURL(r)
}

func (s *Server) handleLiveTVStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, err := s.mediaServer(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	setup, ok := srv.(mediaserver.LiveTVSetup)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}
	st, err := setup.LiveTVStatus(ctx, s.liveTVBase(r))
	if err != nil {
		s.failErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supported": true, "state": st, "tuner": s.liveTVBase(r)})
}

func (s *Server) handleLiveTVSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, err := s.mediaServer(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	setup, ok := srv.(mediaserver.LiveTVSetup)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	st, err := setup.SetUpLiveTV(ctx, s.liveTVBase(r))
	if err != nil {
		s.log.Warn("live TV setup", "err", mediaserver.Redact(err.Error()))
		key := "livetv.failed"
		if s.platform(ctx) == "emby" {
			key = "livetv.failedEmby"
		}
		s.fail(w, r, http.StatusBadGateway, key, platformName(s.platform(ctx)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": st})
}
