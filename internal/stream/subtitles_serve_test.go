package stream

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// TestServeForcedSubtitles plays a film whose forced text track sits inside the
// video and was saved next to it by the subtitle script. Plex answers the
// embedded track with 501 like the real one: the saved file is burned in, from
// Plex once it knows the file, from the media folder before that.
func TestServeForcedSubtitles(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	cue := []byte("1\n00:00:00,500 --> 00:00:03,500\nIIIIIIIIIIII\n")
	embedded := filepath.Join(dir, "embedded.srt")
	os.WriteFile(embedded, cue, 0o644)
	movie := filepath.Join(dir, "Film (2004) Bluray-720p.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=640x360:r=25:d=4", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-i", embedded, "-map", "0:v", "-map", "1:a", "-map", "2:s", "-t", "4", "-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-c:s", "srt", "-metadata:s:a:0", "language=ger", "-disposition:s:0", "forced", movie)
	sidecar := filepath.Join(dir, "Film (2004) Bluray-720p.de.forced.srt")

	const embeddedTrack = `{"id":11,"streamType":3,"index":2,"codec":"srt","languageCode":"deu","forced":true,"title":"Forced Subs"}`
	const fileTrack = `{"id":12,"key":"/library/streams/12","streamType":3,"codec":"srt","languageCode":"deu","forced":true}`
	for _, c := range []struct {
		name    string
		tracks  string // subtitle tracks Plex lists after the audio track
		local   bool   // the media folder is mounted
		sidecar bool   // the script saved the file
		visible bool
	}{
		{"file known to Plex", embeddedTrack + "," + fileTrack, false, true, true},
		{"file not yet known to Plex, media folder mounted", embeddedTrack, true, true, true},
		{"file known to Plex, read from the media folder", embeddedTrack + "," + fileTrack, true, true, true},
		{"no file yet", embeddedTrack, true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(sidecar)
			if c.sidecar {
				os.WriteFile(sidecar, cue, 0o644)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","Media":[{"Part":[{"key":"/library/parts/1","file":"/data/movies/film.mkv",
					"Stream":[{"id":9,"streamType":1,"index":0,"codec":"h264"},{"id":10,"streamType":2,"index":1,"codec":"aac","languageCode":"deu"},`+c.tracks+`]}]}]}]}}`)
			})
			mux.HandleFunc("/library/parts/1", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, movie) })
			mux.HandleFunc("/library/streams/11", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "", http.StatusNotImplemented) })
			mux.HandleFunc("/library/streams/12", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, sidecar) })
			plexSrv := httptest.NewServer(mux)
			defer plexSrv.Close()

			start := time.Now()
			prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(4 * time.Second).UnixMilli(), RatingKey: "1"}
			s := &Streamer{
				FFmpeg: "ffmpeg",
				Server: func(context.Context) (mediaserver.Server, error) {
					return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
				},
				Program: func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
				Media: func(context.Context, string) (*store.Media, error) {
					return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 4000, PartKey: "/library/parts/1"}, nil
				},
				LocalFile: func(context.Context, string) string {
					if c.local {
						return movie
					}
					return ""
				},
				Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
				CacheDir: t.TempDir(),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			rec := httptest.NewRecorder()
			s.Serve(rec, httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx), &store.Channel{ID: 1},
				Options{Mode: "full", NormalizeRes: 720, AudioLanguages: []string{"deu"}})
			out := filepath.Join(dir, "out.ts")
			os.WriteFile(out, rec.Body.Bytes(), 0o644)
			if got := brightIn(t, out, 50, [4]int{560, 650, 160, 50}); got != c.visible {
				t.Errorf("subtitle visible = %v, want %v", got, c.visible)
			}
			if !c.local {
				return
			}
			if _, err := os.Stat(sidecar); c.sidecar && err != nil {
				t.Errorf("the file in the media folder is gone: %v", err)
			}
		})
	}
}
