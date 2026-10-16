package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// Plex fetches the XMLTV guide only once a day. Whenever the schedule changes we
// ask it to reload, so the Plex guide shows what really airs.
const (
	// guideDebounce collects the changes of a re-plan (often several channels)
	// into one reload.
	guideDebounce = 30 * time.Second
	// guideExtendEvery limits reloads when the guide only grows at its end
	// (every 20 minutes): Plex still has hours of guide ahead.
	guideExtendEvery = 6 * time.Hour

	// A reload makes Plex end every running tuner stream, on all channels, so it
	// is skipped while someone watches or records. A stream that just ended
	// counts too: it may be a channel switch.
	guideIdleGrace = 2 * time.Minute
)

var errNoDVR = i18n.E("guide.noDVR")

type guideReloader struct {
	mu     sync.Mutex
	timer  *time.Timer
	last   time.Time // last reload attempt
	status *guideStatus
}

type guideStatus struct {
	At      int64     `json:"at"` // unix seconds
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
	text    i18n.Text // Message before translation
}

// guideChanged schedules a guide reload in Plex (see Scheduler.OnChange).
func (s *Server) guideChanged(extended bool) {
	g := &s.guide
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timer != nil {
		if !extended {
			g.timer.Reset(guideDebounce)
		}
		return
	}
	if extended && time.Since(g.last) < guideExtendEvery {
		return
	}
	g.timer = time.AfterFunc(guideDebounce, s.reloadGuide)
}

// streamsBusy reports whether a stream runs or ended less than guideIdleGrace ago.
func streamsBusy(streams int, lastEnd, now time.Time) bool {
	return streams > 0 || now.Sub(lastEnd) < guideIdleGrace
}

func (s *Server) reloadGuide() {
	g := &s.guide
	now := time.Now()
	g.mu.Lock()
	g.timer, g.last = nil, now
	g.mu.Unlock()

	if streams, lastEnd := s.streamer.Activity(); streamsBusy(streams, lastEnd, now) {
		s.log.Info("Plex guide reload skipped, a stream is running", "streams", streams)
		g.mu.Lock()
		g.status = &guideStatus{At: now.Unix(), text: i18n.M("guide.skipped")}
		g.mu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := s.reloadPlexGuide(ctx)
	if errors.Is(err, library.ErrNotLinked) {
		return
	}
	st := &guideStatus{At: time.Now().Unix(), OK: err == nil}
	if err != nil {
		st.text = i18n.M("%s", err)
		s.log.Warn("reload Plex guide", "err", mediaserver.Redact(err.Error()))
	} else {
		if p := s.platform(ctx); p == "plex" {
			st.text = i18n.M("guide.reloaded", n)
		} else {
			st.text = i18n.M("guide.reloadedServer", platformName(p))
		}
		s.log.Info("reloaded Plex guide", "dvrs", n)
	}
	g.mu.Lock()
	g.status = st
	g.mu.Unlock()
}

// reloadPlexGuide reloads the guide of every DVR that uses our tuner and
// returns how many there were.
func (s *Server) reloadPlexGuide(ctx context.Context) (int, error) {
	srv, err := s.mediaServer(ctx)
	if err != nil {
		return 0, err
	}
	r, ok := srv.(mediaserver.GuideReloader)
	if !ok {
		return 0, nil
	}
	n, err := r.ReloadGuide(ctx, s.deviceID(ctx), s.cfg.BaseURL)
	if err == nil && n == 0 {
		return 0, errNoDVR
	}
	return n, err
}

func (s *Server) guideReloadStatus(l i18n.Lang) *guideStatus {
	s.guide.mu.Lock()
	defer s.guide.mu.Unlock()
	if s.guide.status == nil {
		return nil
	}
	st := *s.guide.status
	st.Message = mediaserver.Redact(st.text.In(l))
	return &st
}
