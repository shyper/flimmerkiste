package server

// "Coming to theaters" slates from TMDb for Jellyfin and Emby, where Plex
// Discover is missing: the owner enters a TMDb key, the upcoming movies of the
// region are read every 12 hours and fill breaks like trailers do.

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/tmdb"
)

// tmdbBase is the TMDb API (tests point it elsewhere).
var tmdbBase = ""

type slateSync struct {
	mu     sync.Mutex
	status discoverStatus
}

// tmdbRegion is the region of the theatrical releases: set, or from the language.
func (s *Server) tmdbRegion(ctx context.Context) string {
	if r := s.store.Setting(ctx, store.SetTMDbRegion, ""); r != "" {
		return r
	}
	if s.lang(ctx) == i18n.DE {
		return "DE"
	}
	return "US"
}

func (s *Server) slateLength(ctx context.Context) time.Duration {
	sec := s.store.SettingInt(ctx, store.SetSlateSeconds, 20)
	return time.Duration(min(max(sec, 8), 60)) * time.Second
}

func (s *Server) slatesWanted(ctx context.Context) bool {
	return s.platform(ctx) != "plex" && s.store.Setting(ctx, store.SetTMDbKey, "") != ""
}

func (s *Server) maybeSlates(ctx context.Context) {
	if !s.slatesWanted(ctx) {
		return
	}
	last := time.Unix(int64(s.store.SettingInt(ctx, store.SetTMDbRefresh, 0)), 0)
	if time.Since(last) > discoverInterval {
		s.startSlates()
	}
}

// startSlates refreshes the slates in the background unless running.
func (s *Server) startSlates() bool {
	s.slates.mu.Lock()
	if s.slates.status.Running {
		s.slates.mu.Unlock()
		return false
	}
	s.slates.status.Running = true
	s.slates.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var res library.DiscoverResult
		var err error
		key := s.store.Setting(ctx, store.SetTMDbKey, "")
		if key == "" {
			err = i18n.E("tmdb.noKey")
		} else {
			lang := "en-US"
			if s.lang(ctx) == i18n.DE {
				lang = "de-DE"
			}
			res, err = s.lib.SyncTMDb(ctx, &tmdb.Client{Key: key, Base: tmdbBase}, s.tmdbRegion(ctx), lang, s.slateLength(ctx))
		}
		_ = s.store.SetSetting(ctx, store.SetTMDbRefresh, strconv.FormatInt(time.Now().Unix(), 10))
		s.slates.mu.Lock()
		s.slates.status = discoverStatus{Last: time.Now().Unix(), Result: res}
		if err != nil {
			s.slates.status.Error = i18n.Message(s.lang(ctx), err)
			s.log.Warn("tmdb slates", "err", err)
		}
		s.slates.mu.Unlock()
	}()
	return true
}

func (s *Server) slatesStatus(ctx context.Context) discoverStatus {
	s.slates.mu.Lock()
	st := s.slates.status
	s.slates.mu.Unlock()
	if st.Last == 0 {
		st.Last = int64(s.store.SettingInt(ctx, store.SetTMDbRefresh, 0))
	}
	return st
}

func (s *Server) handleSlatesRefresh(w http.ResponseWriter, r *http.Request) {
	if s.store.Setting(r.Context(), store.SetTMDbKey, "") == "" {
		s.fail(w, r, http.StatusBadRequest, "tmdb.noKey")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": s.startSlates()})
}
