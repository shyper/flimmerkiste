package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

const (
	sessionCookie = "flimmerkiste_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type ctxKey struct{}

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxKey{}).(*store.User)
	return u
}

func (s *Server) currentUser(r *http.Request) *store.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.store.SessionUser(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	return u
}

// auth wraps an API handler and requires a logged-in user (and optionally admin rights).
func (s *Server) auth(h http.HandlerFunc, admin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			s.fail(w, r, http.StatusUnauthorized, "api.login")
			return
		}
		if admin && !u.IsAdmin() {
			s.fail(w, r, http.StatusForbidden, "api.adminOnly")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner := s.store.Setting(ctx, store.SetOwnerID, "")
	resp := map[string]any{
		"language":     s.reqLang(r),
		"claimed":      owner != "",
		"serverLinked": s.store.Setting(ctx, store.SetServerURL, "") != "",
		"platform":     s.platform(ctx),
		"serverName":   s.store.Setting(ctx, store.SetServerName, ""),
	}
	if u := s.currentUser(r); u != nil {
		resp["user"] = u
	}
	writeJSON(w, http.StatusOK, resp)
}

// plexLogin reports whether people sign in through plex.tv: the instance runs
// with Plex, or nobody has claimed it yet. Jellyfin and Emby instances only
// take accounts of their own server (handleLogin).
func (s *Server) plexLogin(ctx context.Context) bool {
	return s.platform(ctx) == "plex" || s.store.Setting(ctx, store.SetOwnerID, "") == ""
}

func (s *Server) handleCreatePin(w http.ResponseWriter, r *http.Request) {
	if !s.plexLogin(r.Context()) {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	clientID := s.instanceID(r.Context())
	pin, err := plex.CreatePin(r.Context(), clientID)
	if err != nil {
		s.log.Error("create pin", "err", err)
		s.fail(w, r, http.StatusBadGateway, "auth.plexTVDown")
		return
	}
	s.pinsMu.Lock()
	for id, t := range s.pins {
		if time.Since(t) > 20*time.Minute {
			delete(s.pins, id)
		}
	}
	s.pins[pin.ID] = time.Now()
	s.pinsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      pin.ID,
		"authUrl": plex.AuthURL(clientID, pin.Code, ""),
	})
}

func (s *Server) handleCheckPin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.plexLogin(ctx) {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.pinsMu.Lock()
	_, known := s.pins[id]
	s.pinsMu.Unlock()
	if !known {
		s.fail(w, r, http.StatusNotFound, "auth.unknownPin")
		return
	}
	clientID := s.instanceID(ctx)
	pin, err := plex.CheckPin(ctx, clientID, id)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "auth.plexTVDown")
		return
	}
	if pin.AuthToken == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
		return
	}
	s.pinsMu.Lock()
	delete(s.pins, id)
	s.pinsMu.Unlock()

	user, err := s.authorize(ctx, clientID, pin.AuthToken, s.reqLang(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "denied", "error": i18n.Message(s.reqLang(r), err)})
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": user})
}

// startSession signs the user in with a session cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User) error {
	token := randomHex(32)
	if err := s.store.CreateSession(r.Context(), token, user.ID, sessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		Expires: time.Now().Add(sessionTTL),
	})
	return nil
}

var errAccessDenied = i18n.E("auth.denied")

// authorize decides who may log in:
//   - the very first login claims the instance (becomes owner/admin)
//   - the owner is always admin
//   - other accounts need access to the linked Plex server and become viewers
func (s *Server) authorize(ctx context.Context, clientID, plexToken string, lang i18n.Lang) (*store.User, error) {
	acc, err := plex.GetAccount(ctx, clientID, plexToken)
	if err != nil {
		return nil, i18n.E("auth.account")
	}
	owner := s.store.Setting(ctx, store.SetOwnerID, "")
	accID := strconv.FormatInt(acc.ID, 10)
	u := &store.User{Provider: "plex", ExternalID: accID, Username: firstNonEmpty(acc.Title, acc.Username), Email: acc.Email, Thumb: acc.Thumb, Role: "viewer"}

	switch {
	case owner == "":
		// The owner's browser language becomes the installation's.
		if err := s.store.SetSettings(ctx, map[string]string{
			store.SetOwnerID:    accID,
			store.SetOwnerToken: plexToken,
			store.SetLanguage:   string(lang),
		}); err != nil {
			return nil, err
		}
		u.Role = "admin"
	case owner == accID:
		_ = s.store.SetSetting(ctx, store.SetOwnerToken, plexToken)
		u.Role = "admin"
	default:
		if s.store.Setting(ctx, store.SetAllowSharedUser, "1") != "1" {
			return nil, errAccessDenied
		}
		serverID := s.store.Setting(ctx, store.SetServerID, "")
		if serverID == "" {
			return nil, errAccessDenied
		}
		servers, err := plex.Servers(ctx, clientID, plexToken)
		if err != nil {
			return nil, i18n.E("auth.shares")
		}
		ok := false
		for _, srv := range servers {
			if srv.ClientIdentifier == serverID {
				ok = true
				break
			}
		}
		if !ok {
			return nil, errAccessDenied
		}
	}
	if err := s.store.UpsertUser(ctx, u); err != nil {
		return nil, err
	}
	if owner == "" || owner == accID {
		if u.Role != "admin" {
			_ = s.store.SetUserRole(ctx, u.ID, "admin")
			u.Role = "admin"
		}
	}
	return u, nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- server selection during setup ----

type serverChoice struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Connections []string `json:"connections"`
	Suggested   string   `json:"suggested"`
}

func (s *Server) handleSetupServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := s.store.Setting(ctx, store.SetOwnerToken, "")
	servers, err := plex.Servers(ctx, s.instanceID(ctx), token)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "auth.servers")
		return
	}
	var out []serverChoice
	for _, srv := range servers {
		if !srv.Owned {
			continue
		}
		c := serverChoice{ID: srv.ClientIdentifier, Name: srv.Name}
		// Same machine is the common case: try loopback first, then local, then remote.
		c.Connections = append(c.Connections, "http://127.0.0.1:32400", "http://host.docker.internal:32400")
		for _, con := range srv.Connections {
			if con.Local && !con.Relay {
				c.Connections = append(c.Connections, con.URI, "http://"+con.Address+":"+strconv.Itoa(con.Port))
			}
		}
		for _, con := range srv.Connections {
			if !con.Local && !con.Relay {
				c.Connections = append(c.Connections, con.URI)
			}
		}
		c.Suggested = s.probe(ctx, c.Connections, srv.AccessToken, srv.ClientIdentifier)
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

// probe returns the first connection URL that answers with the expected machine id.
func (s *Server) probe(ctx context.Context, urls []string, token, machineID string) string {
	if s.cfg.PlexURL != "" {
		return s.cfg.PlexURL
	}
	for _, u := range urls {
		srv := &plex.Server{BaseURL: u, Token: token, ClientID: s.instanceID(ctx)}
		if id, err := srv.Identity(ctx); err == nil && (machineID == "" || id == machineID) {
			return u
		}
	}
	return ""
}

func (s *Server) handleSetupServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	servers, err := plex.Servers(ctx, s.instanceID(ctx), s.store.Setting(ctx, store.SetOwnerToken, ""))
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "auth.plexTVDown")
		return
	}
	for _, srv := range servers {
		if srv.ClientIdentifier != req.ID || !srv.Owned {
			continue
		}
		url := req.URL
		if url == "" {
			url = s.cfg.PlexURL
		}
		p := &plex.Server{BaseURL: url, Token: srv.AccessToken, ClientID: s.instanceID(ctx)}
		id, err := p.Identity(ctx)
		if err != nil || id != srv.ClientIdentifier {
			s.fail(w, r, http.StatusBadRequest, "auth.unreachable")
			return
		}
		if err := s.store.SetSettings(ctx, map[string]string{
			store.SetServerID:    srv.ClientIdentifier,
			store.SetServerName:  srv.Name,
			store.SetServerURL:   url,
			store.SetServerToken: srv.AccessToken,
		}); err != nil {
			s.failErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.lib.StartSync()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.fail(w, r, http.StatusNotFound, "auth.serverNotFound")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
