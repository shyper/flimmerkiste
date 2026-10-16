package server

import (
	"context"
	"time"

	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/schedule"
)

// stableWindow is how much of the upcoming guide stays untouched when new
// titles arrive, so what was just announced still airs.
const stableWindow = 2 * time.Hour

// applyLibraryChanges re-plans channels affected by a library sync:
//   - removed titles: from the first program that would air one of them
//   - new titles a channel would show: from now + stableWindow
//
// Channels without relevant changes keep their guide.
func (s *Server) applyLibraryChanges(changes library.Changes) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	defer s.sched.EnsureAll(ctx)
	if changes.Empty() {
		return
	}
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	now := time.Now()
	for _, c := range chans {
		cfg, err := schedule.ParseConfig(c.Config)
		if err != nil {
			continue
		}
		var from time.Time
		reason := ""
		if len(changes.Removed) > 0 {
			if t, ok, err := s.store.FirstProgramWith(ctx, c.ID, now, changes.Removed); err == nil && ok {
				from, reason = t, "removed titles"
			}
		}
		if len(changes.Added) > 0 {
			for key := range changes.Added {
				m := s.lib.Get(ctx, key)
				if m != nil && cfg.Wants(c.Kind, m, now) {
					if t := now.Add(stableWindow); from.IsZero() || t.Before(from) {
						from = t
					}
					if reason == "" {
						reason = "new titles"
					} else {
						reason = "new and removed titles"
					}
					break
				}
			}
		}
		if from.IsZero() {
			continue
		}
		s.log.Info("re-planning channel after library change", "channel", c.Name, "reason", reason, "from", from.Format("02.01. 15:04"))
		if err := s.sched.Replan(ctx, c.ID, from); err != nil {
			s.log.Warn("re-plan", "channel", c.Name, "err", err)
		}
	}
}

// libraryKeys returns the rating keys of all items in a library (used when one is disabled).
func (s *Server) libraryKeys(ctx context.Context, id string) map[string]bool {
	keys, err := s.store.MediaKeys(ctx, id)
	if err != nil {
		return nil
	}
	return keys
}
