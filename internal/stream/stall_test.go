package stream

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// TestServeStalledTitle: ffmpeg hangs without sending anything until the client
// gives up. That is logged, the session otherwise ends without a word.
func TestServeStalledTitle(t *testing.T) {
	dir := t.TempDir()
	stall := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(stall, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute)
	prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(time.Hour).UnixMilli(), RatingKey: "1"}
	var logs bytes.Buffer
	s := &Streamer{
		FFmpeg:  stall,
		Program: func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
		Media: func(context.Context, string) (*store.Media, error) {
			return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", PartKey: "http://example.invalid/film.mkv"}, nil
		},
		Log:      slog.New(slog.NewTextHandler(&logs, nil)),
		CacheDir: dir,
	}
	ctx, cancel := context.WithTimeout(context.Background(), silentWait+time.Second)
	defer cancel()
	req := httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx)
	s.Serve(httptest.NewRecorder(), req, &store.Channel{ID: 1, Name: "Test"}, Options{Mode: "copy"})
	if !strings.Contains(logs.String(), "had not sent anything") || !strings.Contains(logs.String(), "title=Film") {
		t.Errorf("stall not logged:\n%s", logs.String())
	}
}
