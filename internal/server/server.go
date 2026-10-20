// Package server wires everything together behind one HTTP listener:
// the HDHomeRun/XMLTV endpoints Plex talks to, and the authenticated web UI + API.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/ai"
	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	lib      *library.Library
	sched    *schedule.Scheduler
	streamer *stream.Streamer
	vaapi    *stream.VAAPI
	log      *slog.Logger
	web      fs.FS
	// container: running in a container, where media folders are assigned (paths.go)
	container bool

	pinsMu sync.Mutex
	pins   map[int64]time.Time

	logoMu sync.Mutex

	discoverMu sync.Mutex
	discover   discoverStatus

	guide guideReloader

	slates slateSync
	folder slateSync // the trailer folder (FLIMMERKISTE_TRAILER)
	info   infoState

	// clock replaces time.Now in tests (nil = the real time).
	clock func() time.Time
}

// Version is the build version, shown in the settings (set by main).
var Version = "dev"

// lang is the language of the installation: of the TV picture, the guide and
// server messages (English until one is set).
func (s *Server) lang(ctx context.Context) i18n.Lang {
	if v := s.store.Setting(ctx, store.SetLanguage, ""); i18n.Known(v) {
		return i18n.Lang(v)
	}
	return i18n.EN
}

// reqLang is the language for answers to a request: the installation's, or
// before the owner set one, the browser's.
func (s *Server) reqLang(r *http.Request) i18n.Lang {
	if v := s.store.Setting(r.Context(), store.SetLanguage, ""); i18n.Known(v) {
		return i18n.Lang(v)
	}
	return i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// fail answers with a translated error message.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, key string, args ...any) {
	writeErr(w, status, i18n.T(s.reqLang(r), key, args...))
}

// failErr answers with the (translated) message of err.
func (s *Server) failErr(w http.ResponseWriter, r *http.Request, status int, err error) {
	writeErr(w, status, i18n.Message(s.reqLang(r), err))
}

// now is the current time; tests fix it through clock.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func New(cfg config.Config, st *store.Store, log *slog.Logger, web fs.FS) (*Server, error) {
	s := &Server{cfg: cfg, store: st, log: log, web: web, pins: map[int64]time.Time{}, container: inContainer()}
	for _, dir := range []string{"logos", "cache"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, dir), 0o755); err != nil {
			return nil, err
		}
	}
	ctx := context.Background()
	if id, _ := st.GetSetting(ctx, store.SetInstanceID); id == "" {
		if err := st.SetSetting(ctx, store.SetInstanceID, randomHex(12)); err != nil {
			return nil, err
		}
	}
	s.lib = library.New(st, s.mediaServer, log)
	if err := s.migrateConfigs(ctx); err != nil {
		return nil, i18n.E("server.migrate", err)
	}
	s.sched = schedule.New(st, s.lib, s.aiProvider, time.Duration(cfg.Horizon)*time.Hour, log)
	s.sched.OnChange = s.guideChanged
	s.lib.OnSynced(func(c library.Changes) { go s.applyLibraryChanges(c) })
	s.streamer = &stream.Streamer{
		FFmpeg:   cfg.FFmpeg,
		Server:   s.mediaServer,
		Program:  s.programAt,
		Media:    s.playable,
		Log:      log,
		CacheDir: filepath.Join(cfg.DataDir, "cache"),
	}
	s.vaapi = &stream.VAAPI{Device: cfg.VAAPI, NVIDIA: cfg.NVIDIA, V4L2: cfg.V4L2, FFmpeg: cfg.FFmpeg}
	s.streamer.Card = s.renderCard
	s.streamer.CardPages = s.renderCardPages
	s.streamer.CardAudio = s.cardAudio
	s.streamer.Caption = s.renderCaption
	s.streamer.Credits = s.renderCredits
	s.streamer.CreditsCard = s.renderCreditsCard
	s.streamer.Clock = s.renderClock
	s.streamer.ClockText = func(t time.Time) string { return i18n.Clock(s.lang(context.Background()), t) }
	s.streamer.LocalFile = s.localFile
	s.streamer.Aired = func(ctx context.Context, ch *store.Channel, prog *store.Program, failed bool) {
		if err := s.store.MarkHistory(ctx, ch.ID, prog.Start, failed); err != nil && ctx.Err() == nil {
			s.log.Warn("history", "channel", ch.Name, "err", err)
		}
	}
	s.streamer.Prefetch = s.prefetchURL
	s.streamer.Substitute = s.sched.Substitute
	return s, nil
}

// migrateConfigs brings stored channel configurations into the current format
// after an update: ParseConfig fills in what older versions did not store, and
// the result is written back, so the database and the editor see one format.
func (s *Server) migrateConfigs(ctx context.Context) error {
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return err
	}
	catalog, _ := s.lib.All(ctx)
	n := 0
	for _, c := range chans {
		cfg, err := schedule.ParseConfig(c.Config)
		if err != nil {
			s.log.Warn("channel config unreadable, left as is", "channel", c.Name, "err", err)
			continue
		}
		cfg.FillYears(c.Kind, catalog)
		raw, err := json.Marshal(cfg)
		if err != nil || bytes.Equal(raw, c.Config) {
			continue
		}
		if err := s.store.SetChannelConfig(ctx, c.ID, raw); err != nil {
			return err
		}
		n++
	}
	if n > 0 {
		s.log.Info("migrated channel configs to the current format", "channels", n)
	}
	return nil
}

// Background starts the scheduler and the periodic library sync.
func (s *Server) Background(ctx context.Context) {
	go s.sched.Run(ctx)
	go func() {
		if ok, status := s.vaapi.Available(ctx); ok {
			_, decode := s.vaapi.Decode(ctx)
			s.log.Info("hardware encoding available", "device", status.In(i18n.EN), "decoding", decode.In(i18n.EN))
		} else {
			s.log.Info("hardware encoding not available, using software", "reason", status.In(i18n.EN))
		}
	}()
	if url, _ := s.store.GetSetting(ctx, store.SetServerURL); url != "" {
		s.lib.StartTrailerSync()
	}
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			s.maybeSync(ctx)
			go s.refreshDetails(ctx)
			s.maybeDiscover(ctx)
			s.maybeSlates(ctx)
			s.syncFolder(ctx)
			if _, err := s.store.RecordHistory(ctx, time.Now()); err != nil {
				s.log.Warn("history", "err", err)
			}
			s.maybeInfo(ctx)
			_ = s.store.PurgeSessions(ctx)
			s.purgeCache()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// refreshDetails fetches the full metadata of titles that are due again, a
// few at a time (see library.RefreshDetails).
func (s *Server) refreshDetails(ctx context.Context) {
	if url, _ := s.store.GetSetting(ctx, store.SetServerURL); url == "" {
		return
	}
	if err := s.lib.RefreshDetails(ctx); err != nil && !errors.Is(err, library.ErrNotLinked) && ctx.Err() == nil {
		s.log.Warn("refresh details", "err", err)
	}
}

func (s *Server) maybeSync(ctx context.Context) {
	if url, _ := s.store.GetSetting(ctx, store.SetServerURL); url == "" {
		return
	}
	interval := time.Duration(s.store.SettingInt(ctx, store.SetSyncInterval, 6)) * time.Hour
	last := time.Unix(int64(s.store.SettingInt(ctx, store.SetLastSync, 0)), 0)
	if interval > 0 && time.Since(last) > interval {
		s.lib.StartSync()
	}
}

func (s *Server) instanceID(ctx context.Context) string {
	return s.store.Setting(ctx, store.SetInstanceID, "flimmerkiste")
}

func (s *Server) plexServer(ctx context.Context) (*plex.Server, error) {
	url := s.cfg.PlexURL
	if url == "" {
		url = s.store.Setting(ctx, store.SetServerURL, "")
	}
	token := s.store.Setting(ctx, store.SetServerToken, "")
	if url == "" || token == "" {
		return nil, library.ErrNotLinked
	}
	return &plex.Server{BaseURL: url, Token: token, ClientID: s.instanceID(ctx)}, nil
}

// mediaServer returns the linked media server.
func (s *Server) mediaServer(ctx context.Context) (mediaserver.Server, error) {
	if s.platform(ctx) != "plex" {
		return s.jellyfinClient(ctx)
	}
	p, err := s.plexServer(ctx)
	if err != nil {
		return nil, err
	}
	return plex.NewBackend(p, s.log), nil
}

func (s *Server) aiProvider(ctx context.Context) (ai.Provider, error) {
	return ai.New(ai.Settings{
		Provider: s.store.Setting(ctx, store.SetAIProvider, ""),
		APIKey:   s.store.Setting(ctx, store.SetAIKey, ""),
		Model:    s.store.Setting(ctx, store.SetAIModel, ""),
		BaseURL:  s.store.Setting(ctx, store.SetAIBaseURL, ""),
	})
}

// busySlate: how long the "up next" slate fills in while a channel is being
// planned, before the stream looks for its program again.
const busySlate = 15 * time.Second

// programAt looks up the airing program, generating the guide on demand. A
// stream never waits for a channel that is being planned (an AI channel may
// take minutes): the slate fills in meanwhile.
func (s *Server) programAt(ctx context.Context, channelID int64, t time.Time) (*store.Program, error) {
	p, err := s.store.ProgramAt(ctx, channelID, t)
	if errors.Is(err, store.ErrNotFound) {
		if err := s.sched.EnsureNow(ctx, channelID); errors.Is(err, schedule.ErrBusy) {
			return &store.Program{ChannelID: channelID, Start: t.UnixMilli(), Stop: t.Add(busySlate).UnixMilli(),
				RatingKey: store.CardKey, Kind: store.KindCard}, nil
		} else if err != nil {
			s.log.Warn("ensure schedule", "err", err)
		}
		p, err = s.store.ProgramAt(ctx, channelID, t)
	}
	if err == nil && p.Kind == store.KindPreview {
		// Not bound yet (e.g. right after a re-plan): pick its trailer now.
		if err := s.sched.BindPreviewsNow(ctx, channelID, p.StopTime()); errors.Is(err, schedule.ErrBusy) {
			// the slot stays a plain slate this time
			card := *p
			card.RatingKey, card.Kind = store.CardKey, store.KindCard
			return &card, nil
		} else if err != nil {
			s.log.Warn("bind previews", "err", err)
		}
		p, err = s.store.ProgramAt(ctx, channelID, t)
	}
	return p, err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- Plex-facing endpoints (no login; Plex cannot authenticate) ---
	// Restricted to the local network; logos and images stay open because the web UI uses them.
	// The guide holds no secrets (titles, descriptions, proxied images), so other
	// EPG clients may fetch it from anywhere, including through a reverse proxy.
	guard := newTunerGuard(s.cfg.TunerAllow, s.log)
	mux.HandleFunc("GET /discover.json", guard.wrap(s.handleDiscover))
	mux.HandleFunc("GET /device.xml", guard.wrap(s.handleDeviceXML))
	mux.HandleFunc("GET /lineup_status.json", guard.wrap(s.handleLineupStatus))
	mux.HandleFunc("GET /lineup.json", guard.wrap(s.handleLineup))
	mux.HandleFunc("POST /lineup.post", guard.wrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	mux.HandleFunc("GET /stream/{number}", guard.wrap(s.handleStream))
	mux.HandleFunc("GET /auto/{vnumber}", guard.wrap(s.handleStream))
	mux.HandleFunc("GET /epg.xml", s.handleXMLTV)
	mux.HandleFunc("GET /logos/{file}", s.handleLogo)
	mux.HandleFunc("GET /img/{key}/{kind}", s.handleImage)

	// --- auth ---
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/pin", s.handleCreatePin)
	mux.HandleFunc("GET /api/auth/pin/{id}", s.handleCheckPin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/setup/connect", s.handleSetupConnect)

	// --- API (viewer) ---
	mux.Handle("GET /api/meta", s.auth(s.handleMeta, false))
	mux.Handle("GET /api/channels", s.auth(s.handleListChannels, false))
	mux.Handle("GET /api/channels/{id}", s.auth(s.handleGetChannel, false))
	mux.Handle("GET /api/guide", s.auth(s.handleGuide, false))
	mux.Handle("GET /api/sessions", s.auth(s.handleSessions, false))
	mux.Handle("GET /api/history", s.auth(s.handleHistory, true))

	// --- API (admin) ---
	mux.Handle("GET /api/setup/servers", s.auth(s.handleSetupServers, true))
	mux.Handle("POST /api/setup/server", s.auth(s.handleSetupServer, true))
	mux.Handle("GET /api/settings", s.auth(s.handleGetSettings, true))
	mux.Handle("PUT /api/settings", s.auth(s.handlePutSettings, true))
	mux.Handle("POST /api/ai/test", s.auth(s.handleAITest, true))
	mux.Handle("GET /api/users", s.auth(s.handleUsers, true))
	mux.Handle("PUT /api/users/{id}", s.auth(s.handlePutUser, true))
	mux.Handle("DELETE /api/users/{id}", s.auth(s.handleDeleteUser, true))
	mux.Handle("GET /api/libraries", s.auth(s.handleLibraries, true))
	mux.Handle("PUT /api/libraries/{id}", s.auth(s.handlePutLibrary, true))
	mux.Handle("PUT /api/libraries/{id}/paths", s.auth(s.handlePutLibraryPaths, true))
	mux.Handle("POST /api/libraries/{id}/fullscan", s.auth(s.handleFullScan, true))
	mux.Handle("POST /api/library/sync", s.auth(s.handleSync, true))
	mux.Handle("GET /api/library/status", s.auth(s.handleSyncStatus, true))
	mux.Handle("GET /api/library/facets", s.auth(s.handleFacets, true))
	mux.Handle("GET /api/library/search", s.auth(s.handleSearch, true))
	mux.Handle("GET /api/library/show/{key}", s.auth(s.handleShow, true))
	mux.Handle("GET /api/trailers", s.auth(s.handleTrailers, true))
	mux.Handle("GET /api/trailers/hubs", s.auth(s.handleTrailerHubs, true))
	mux.Handle("POST /api/trailers/refresh", s.auth(s.handleTrailerRefresh, true))
	mux.Handle("POST /api/preview", s.auth(s.handlePreview, true))
	mux.Handle("GET /api/presets/seasonal", s.auth(s.handleSeasonalPreset, true))
	mux.Handle("GET /api/export", s.auth(s.handleExport, true))
	mux.Handle("GET /api/livetv", s.auth(s.handleLiveTVStatus, true))
	mux.Handle("POST /api/slates/refresh", s.auth(s.handleSlatesRefresh, true))
	mux.Handle("GET /api/info/places", s.auth(s.handlePlaces, true))
	mux.Handle("POST /api/info/refresh", s.auth(s.handleInfoRefresh, true))
	mux.Handle("POST /api/livetv/setup", s.auth(s.handleLiveTVSetup, true))
	mux.Handle("POST /api/import/preview", s.auth(s.handleImportPreview, true))
	mux.Handle("POST /api/import", s.auth(s.handleImport, true))
	mux.Handle("GET /api/logo/preview", s.auth(s.handleLogoPreview, true))
	mux.Handle("POST /api/poster-color", s.auth(s.handlePosterColor, true))
	mux.Handle("POST /api/channels", s.auth(s.handleCreateChannel, true))
	mux.Handle("POST /api/channels/regenerate", s.auth(s.handleRegenerateAll, true))
	mux.Handle("PUT /api/channels/{id}", s.auth(s.handleUpdateChannel, true))
	mux.Handle("DELETE /api/channels/{id}", s.auth(s.handleDeleteChannel, true))
	mux.Handle("POST /api/channels/{id}/logo", s.auth(s.handleUploadLogo, true))
	mux.Handle("DELETE /api/channels/{id}/logo", s.auth(s.handleDeleteLogo, true))
	mux.Handle("POST /api/channels/{id}/regenerate", s.auth(s.handleRegenerate, true))
	mux.Handle("GET /api/channels/{id}/error", s.auth(s.handleChannelError, true))
	mux.Handle("DELETE /api/channels/{id}/error", s.auth(s.handleDismissError, true))
	mux.Handle("GET /api/channels/{id}/card", s.auth(s.handleCardPreview, true))
	mux.Handle("GET /api/channels/{id}/credits", s.auth(s.handleCreditsPreview, true))

	// --- single page app ---
	mux.Handle("/", s.spa())

	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject cross-site state changing requests (session cookies are SameSite=Lax as well).
		if r.Method != http.MethodGet && r.Method != http.MethodHead && strings.HasPrefix(r.URL.Path, "/api/") {
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) && !s.cfg.DevCORS {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	o := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return o == host
}

// spa serves the embedded frontend, falling back to index.html for client-side routes.
func (s *Server) spa() http.Handler {
	files := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(s.web, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		data, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, i18n.T(s.reqLang(r), "api.noFrontend"), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	})
}

// baseURL is how Plex (and browsers) reach us.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return strings.TrimRight(s.cfg.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return readJSONLimit(r, v, 4<<20)
}

// readJSONLimit decodes a request body of at most limit bytes.
func readJSONLimit(r *http.Request, v any, limit int64) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit)).Decode(v)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
