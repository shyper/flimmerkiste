package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// playable resolves a program's rating key: library items, or trailers in the
// configured picture height.
func (s *Server) playable(ctx context.Context, key string) (*store.Media, error) {
	if strings.HasPrefix(key, store.TrailerPrefix) {
		t, err := s.store.GetTrailer(ctx, key)
		if err != nil {
			return nil, err
		}
		return t.Media(s.store.SettingInt(ctx, store.SetNormalizeRes, 720)), nil
	}
	if m := s.lib.Get(ctx, key); m != nil {
		return m, nil
	}
	return s.store.GetMedia(ctx, key)
}

// prefetchURL returns the download URL of an upcoming trailer, so the streamer
// can fetch it while the previous item plays.
func (s *Server) prefetchURL(ctx context.Context, prog *store.Program) string {
	if !strings.HasPrefix(prog.RatingKey, store.TrailerPrefix) {
		return ""
	}
	m, err := s.playable(ctx, prog.RatingKey)
	if err != nil || m.PartKey == "" || store.LocalFile(m.PartKey) != "" {
		return ""
	}
	if isAbsolute(m.PartKey) {
		return m.PartKey
	}
	srv, err := s.mediaServer(ctx)
	if err != nil {
		return ""
	}
	return srv.StreamURL(m.PartKey)
}

// syncFolder reads the trailer folder again (every 10 minutes: it only changes
// when scripts/trailers.py runs).
func (s *Server) syncFolder(ctx context.Context) {
	if s.cfg.Trailers == "" {
		return
	}
	res, err := s.lib.SyncFolder(ctx, s.cfg.Trailers)
	st := discoverStatus{Last: time.Now().Unix(), Result: res}
	if err != nil {
		err = i18n.E("trailers.folder", s.cfg.Trailers, err)
		st.Error = i18n.Message(s.lang(ctx), err)
		s.log.Warn("trailer folder", "err", i18n.Message(i18n.EN, err))
	}
	s.folder.mu.Lock()
	s.folder.status = st
	s.folder.mu.Unlock()
}

// folderStatus describes the trailer folder for the settings page (nil = none).
func (s *Server) folderStatus() any {
	if s.cfg.Trailers == "" {
		return nil
	}
	s.folder.mu.Lock()
	defer s.folder.mu.Unlock()
	return map[string]any{"dir": s.cfg.Trailers, "last": s.folder.status.Last, "error": s.folder.status.Error, "result": s.folder.status.Result}
}

// imageURL turns an artwork path into a fetchable URL: Plex paths go through
// the photo transcoder, Discover artwork is public already.
func (s *Server) imageURL(ctx context.Context, path string, width, height int) (string, error) {
	if isAbsolute(path) {
		return path, nil
	}
	srv, err := s.mediaServer(ctx)
	if err != nil {
		return "", err
	}
	return srv.ImageURL(path, width, height), nil
}

func isAbsolute(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// discoverInterval: how often the Plex Discover lists are read again.
const discoverInterval = 12 * time.Hour

type discoverStatus struct {
	Running bool                   `json:"running"`
	Last    int64                  `json:"last,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Result  library.DiscoverResult `json:"result"`
}

func (s *Server) discoverClient(ctx context.Context) (mediaserver.TrailerCatalog, error) {
	token := s.store.Setting(ctx, store.SetOwnerToken, "")
	if token == "" {
		return nil, i18n.E("discover.noAccount")
	}
	lang := "de"
	if langs := s.sched.TrailerLanguages(ctx); len(langs) > 0 {
		lang = store.LanguageTag(langs[0])
	}
	return plex.NewCatalog(&plex.Discover{Token: token, ClientID: s.instanceID(ctx), Language: lang}), nil
}

// trailerHubs are the Discover lists trailers are taken from (none = off).
func (s *Server) trailerHubs(ctx context.Context) []string {
	out := []string{}
	for _, h := range strings.Split(s.store.Setting(ctx, store.SetTrailerHubs, ""), ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func (s *Server) maybeDiscover(ctx context.Context) {
	if len(s.trailerHubs(ctx)) == 0 {
		return
	}
	last := time.Unix(int64(s.store.SettingInt(ctx, store.SetTrailerRefresh, 0)), 0)
	if time.Since(last) > discoverInterval {
		s.startDiscover()
	}
}

// startDiscover refreshes the Discover trailers in the background unless running.
func (s *Server) startDiscover() bool {
	s.discoverMu.Lock()
	if s.discover.Running {
		s.discoverMu.Unlock()
		return false
	}
	s.discover.Running = true
	s.discoverMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		var res library.DiscoverResult
		d, err := s.discoverClient(ctx)
		if err == nil {
			res, err = s.lib.SyncDiscover(ctx, d, s.trailerHubs(ctx), s.sched.TrailerLanguages(ctx))
		}
		// A failed run is retried at the next interval, not every few minutes.
		_ = s.store.SetSetting(ctx, store.SetTrailerRefresh, strconv.FormatInt(time.Now().Unix(), 10))
		s.discoverMu.Lock()
		s.discover = discoverStatus{Last: time.Now().Unix(), Result: res}
		if err != nil {
			s.discover.Error = err.Error()
			s.log.Warn("discover trailers", "err", err)
		}
		s.discoverMu.Unlock()
	}()
	return true
}

func (s *Server) handleTrailers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats, err := s.store.TrailerStats(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.discoverMu.Lock()
	ds := s.discover
	s.discoverMu.Unlock()
	if ds.Last == 0 {
		ds.Last = int64(s.store.SettingInt(ctx, store.SetTrailerRefresh, 0))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats":     stats,
		"library":   s.lib.Status().Trailers,
		"discover":  ds,
		"slates":    s.slatesStatus(ctx),
		"folder":    s.folderStatus(),
		"info":      s.infoStatus(ctx),
		"hubs":      s.trailerHubs(ctx),
		"languages": s.sched.TrailerLanguages(ctx),
	})
}

// handleTrailerHubs lists the Discover lists of the account to choose from.
func (s *Server) handleTrailerHubs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	d, err := s.discoverClient(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	hubs, err := d.Lists(ctx)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "discover.unreachable", err)
		return
	}
	out := []mediaserver.CatalogList{}
	for _, h := range hubs {
		if h.Type != "directory" {
			out = append(out, h)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTrailerRefresh(w http.ResponseWriter, r *http.Request) {
	if len(s.trailerHubs(r.Context())) == 0 {
		s.fail(w, r, http.StatusBadRequest, "discover.pickLists")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": s.startDiscover()})
}
