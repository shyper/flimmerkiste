package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/store"
)

// The EPG prefix must reach Plex as the GuideNumber and match the XMLTV channel id,
// like Cabernet and Threadfin do; otherwise Plex shows the plain number.
func TestGuideNumberPrefix(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st.SetSettings(ctx, map[string]string{store.SetEPGPrefix: "fk-"})
	st.CreateChannel(ctx, &store.Channel{Number: 7, Name: "Sieben", Enabled: true, Kind: "manual", Config: json.RawMessage(`{}`)})
	srv, err := New(config.Config{DataDir: dir, Horizon: 24}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	srv.handleLineup(w, httptest.NewRequest("GET", "/lineup.json", nil))
	var lineup []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &lineup); err != nil {
		t.Fatal(err)
	}
	if len(lineup) != 1 || lineup[0]["GuideNumber"] != "fk-7" {
		t.Fatalf("lineup = %v, want GuideNumber fk-7", lineup)
	}

	w = httptest.NewRecorder()
	srv.handleXMLTV(w, httptest.NewRequest("GET", "/xmltv.xml", nil))
	for _, want := range []string{`<channel id="fk-7">`, `<display-name>Sieben</display-name>`,
		`<display-name>fk-7 Sieben</display-name>`, `<display-name>fk-7</display-name>`, `<lcn>fk-7</lcn>`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("xmltv lacks %s:\n%s", want, w.Body.String())
		}
	}

	// Tuning by GuideNumber resolves the channel; an unknown one still 404s.
	for path, want := range map[string]int{"/auto/vfk-7": 503, "/auto/vfk-8": 404} {
		st.SetSettings(ctx, map[string]string{store.SetTunerCount: "0"}) // stop before ffmpeg
		w = httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "127.0.0.1:50000" // the tuner guard only admits local networks
		srv.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s = %d, want %d", path, w.Code, want)
		}
	}
}

// The guide is public: external EPG clients may fetch it through a reverse proxy,
// while streams and lineup stay limited to the local network.
func TestGuidePublicBehindProxy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(config.Config{DataDir: dir, Horizon: 24}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/epg.xml": 200, "/lineup.json": 403, "/stream/1": 403} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "203.0.113.7:50000"
		r.Header.Set("X-Forwarded-For", "198.51.100.1")
		srv.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s = %d, want %d", path, w.Code, want)
		}
	}
}
