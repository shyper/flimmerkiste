package server

import (
	"context"
	"time"

	"github.com/shyper/flimmerkiste/internal/library"
)

// applyLibraryChanges brings a library sync into the guides without changing
// what has been announced: new titles join as the guides are extended at
// their end, and only the slots of removed titles are filled with clips.
func (s *Server) applyLibraryChanges(changes library.Changes) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	defer s.sched.EnsureAll(ctx)
	if len(changes.Removed) == 0 {
		return
	}
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	for _, c := range chans {
		n, err := s.sched.ReplaceRemoved(ctx, c.ID, changes.Removed)
		if err != nil {
			s.log.Warn("replace removed titles", "channel", c.Name, "err", err)
		} else if n > 0 {
			s.log.Info("filled the slots of removed titles", "channel", c.Name, "slots", n)
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
