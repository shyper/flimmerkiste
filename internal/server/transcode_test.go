package server

import (
	"context"
	"testing"

	"github.com/shyper/flimmerkiste/internal/store"
)

func TestTranscodeModeDefault(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{store: st}
	ctx := context.Background()
	if got := s.transcodeMode(ctx, "default"); got != "copy" {
		t.Fatalf("without setting: %q", got)
	}
	_ = st.SetSetting(ctx, store.SetTranscode, "full")
	if got := s.transcodeMode(ctx, "default"); got != "full" {
		t.Fatalf("global full: %q", got)
	}
	// Channels with an explicit mode ignore the global setting.
	if got := s.transcodeMode(ctx, "audio"); got != "audio" {
		t.Fatalf("explicit: %q", got)
	}
	_ = st.SetSetting(ctx, store.SetTranscode, "bogus")
	if got := s.transcodeMode(ctx, "default"); got != "copy" {
		t.Fatalf("invalid setting: %q", got)
	}
}
