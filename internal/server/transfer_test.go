package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
)

// transferWorld is an installation with a small library; keys differ per
// installation, external ids and titles do not.
func transferWorld(t *testing.T, keyPrefix string, extra ...*store.Media) (*Server, http.Handler) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetSettings(ctx, map[string]string{store.SetServerURL: "http://plex.invalid", store.SetServerToken: "x", store.SetOwnerID: "1", store.SetLanguage: "de"})
	st.UpsertLibrary(ctx, store.Library{ID: keyPrefix + "lib1", Title: "Filme", Type: "movie"})
	st.UpsertLibrary(ctx, store.Library{ID: keyPrefix + "lib2", Title: "Serien", Type: "show"})
	k := func(s string) string { return keyPrefix + s }
	items := []*store.Media{
		{RatingKey: k("1"), LibraryID: k("lib1"), Type: "movie", Title: "Matrix", Year: 1999, DurationMs: 8e6, Ext: map[string]string{"tmdb": "603", "imdb": "tt0133093"}},
		{RatingKey: k("2"), LibraryID: k("lib1"), Type: "movie", Title: "Ghostbusters", Year: 1984, DurationMs: 6e6},
		{RatingKey: k("e1"), LibraryID: k("lib2"), Type: "episode", Title: "Pilot", ShowKey: k("s1"), ShowTitle: "Die Simpsons", Season: 1, Episode: 1, DurationMs: 1e6,
			Ext: map[string]string{"tvdb": "55452", "show.tvdb": "71663"}},
		{RatingKey: k("e2"), LibraryID: k("lib2"), Type: "episode", Title: "Bart, das Genie", ShowKey: k("s1"), ShowTitle: "Die Simpsons", Season: 1, Episode: 2, DurationMs: 1e6,
			Ext: map[string]string{"show.tvdb": "71663"}},
	}
	if err := st.UpsertMedia(ctx, append(items, extra...), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Provider: "plex", ExternalID: "1", Username: "owner", Role: "admin"}
	st.UpsertUser(ctx, u)
	st.CreateSession(ctx, "s", u.ID, time.Hour)
	srv, err := New(config.Config{DataDir: dir, Horizon: 6}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler()
}

func call(t *testing.T, h http.Handler, method, path string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "s"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code
}

func TestExportImport(t *testing.T) {
	_, a := transferWorld(t, "a")
	manual := map[string]any{"name": "Klassiker", "number": 5, "kind": "manual", "config": map[string]any{"order": "fixed",
		"source": map[string]any{"type": "manual", "items": []map[string]any{
			{"key": "a1", "type": "movie", "title": "Matrix", "year": 1999},
			{"key": "a2", "type": "movie", "title": "Ghostbusters", "year": 1984},
			{"key": "as1", "type": "show", "title": "Die Simpsons"},
			{"key": "as1", "type": "season", "title": "Die Simpsons", "season": 1},
			{"key": "ae2", "type": "episode", "title": "Bart, das Genie"},
			{"key": "a9", "type": "movie", "title": "Gibt es nicht", "year": 2001},
		}}}}
	smart := map[string]any{"name": "Nur Filme", "number": 7, "kind": "smart", "config": map[string]any{
		"source": map[string]any{"type": "rules", "rules": map[string]any{"match": "all", "rules": []map[string]any{
			{"field": "library", "op": "is", "values": []string{"alib1"}}}}}}}
	for _, c := range []any{manual, smart} {
		if code := call(t, a, "POST", "/api/channels", c, nil); code != http.StatusCreated {
			t.Fatalf("create: %d", code)
		}
	}
	var file exportFile
	call(t, a, "GET", "/api/export", nil, &file)
	if file.Format != exportFormat || len(file.Channels) != 2 {
		t.Fatalf("export: %+v", file)
	}
	if got := file.Channels[0].Titles["movie|a1|0"]; got.IDs["tmdb"] != "603" || got.Title != "Matrix" {
		t.Errorf("matrix: %+v", got)
	}
	if got := file.Channels[0].Titles["show|as1|0"]; got.ShowIDs["tvdb"] != "71663" {
		t.Errorf("show: %+v", got)
	}

	// another installation: other keys, a channel already on number 5
	srvB, b := transferWorld(t, "b")
	call(t, b, "POST", "/api/channels", map[string]any{"name": "Vorhanden", "number": 5, "kind": "smart",
		"config": map[string]any{"source": map[string]any{"type": "rules"}}}, nil)
	var preview struct {
		Channels []importPreview `json:"channels"`
	}
	call(t, b, "POST", "/api/import/preview", file, &preview)
	if len(preview.Channels) != 2 || preview.Channels[0].Conflict != "Vorhanden" ||
		len(preview.Channels[0].Missing) != 1 || preview.Channels[0].Missing[0] != "Gibt es nicht" {
		t.Fatalf("preview: %+v", preview.Channels)
	}

	raw, _ := json.Marshal(file)
	var res struct {
		Created, Replaced, Skipped int
		Missing, Errors            []string
	}
	call(t, b, "POST", "/api/import", map[string]any{"file": json.RawMessage(raw),
		"choices": []importChoice{{Index: 0, Action: "new"}, {Index: 1, Action: "new"}}}, &res)
	if res.Created != 2 || len(res.Errors) != 0 {
		t.Fatalf("import: %+v", res)
	}
	chans, _ := srvB.store.ListChannels(context.Background())
	byName := map[string]*store.Channel{}
	for _, c := range chans {
		byName[c.Name] = c
	}
	k := byName["Klassiker"]
	if k == nil || k.Number == 5 {
		t.Fatalf("number conflict: %+v", k)
	}
	cfg, _ := schedule.ParseConfig(k.Config)
	var keys []string
	for _, it := range cfg.Source.Items {
		keys = append(keys, it.Type+":"+it.Key)
	}
	if got := strings.Join(keys, " "); got != "movie:b1 movie:b2 show:bs1 season:bs1 episode:be2" {
		t.Errorf("mapped items: %s", got)
	}
	n := byName["Nur Filme"]
	cfg, _ = schedule.ParseConfig(n.Config)
	if v := cfg.Source.Rules.Rules[0].Values; len(v) != 1 || v[0] != "blib1" {
		t.Errorf("library rule: %v", v)
	}

	// replace takes over the channel with the number
	call(t, b, "POST", "/api/import", map[string]any{"file": json.RawMessage(raw),
		"choices": []importChoice{{Index: 0, Action: "replace"}}}, &res)
	if res.Replaced != 1 || res.Skipped != 1 {
		t.Fatalf("replace: %+v", res)
	}
	five, _ := srvB.store.GetChannelByNumber(context.Background(), 5)
	if five.Name != "Klassiker" || five.Kind != "manual" {
		t.Errorf("replaced: %+v", five)
	}

	// bad files
	var e struct{ Error string }
	if code := call(t, b, "POST", "/api/import/preview", map[string]any{"format": "other"}, &e); code != http.StatusBadRequest || e.Error == "" {
		t.Errorf("bad file: %d %q", code, e.Error)
	}
	file.Version = exportVersion + 1
	if code := call(t, b, "POST", "/api/import/preview", file, &e); code != http.StatusBadRequest || !strings.Contains(e.Error, "neueren") {
		t.Errorf("newer file: %d %q", code, e.Error)
	}
}

// A library filter whose library is not found here must not turn into "every
// library": the rule keeps a value no library has, and the import says so.
func TestImportMissingLibraryKeepsFilter(t *testing.T) {
	_, a := transferWorld(t, "a")
	call(t, a, "POST", "/api/channels", map[string]any{"name": "Nur Filme", "number": 7, "kind": "smart", "config": map[string]any{
		"source": map[string]any{"type": "rules", "rules": map[string]any{"match": "all", "rules": []map[string]any{
			{"field": "library", "op": "is", "values": []string{"alib1"}}}}}}}, nil)
	var file exportFile
	call(t, a, "GET", "/api/export", nil, &file)

	srvB, b := transferWorld(t, "b")
	ctx := context.Background()
	srvB.store.UpsertLibrary(ctx, store.Library{ID: "blib1", Title: "Spielfilme", Type: "movie"}) // named differently here
	raw, _ := json.Marshal(file)
	var res struct {
		Created int
		Missing []string
	}
	call(t, b, "POST", "/api/import", map[string]any{"file": json.RawMessage(raw), "choices": []importChoice{{Index: 0, Action: "new"}}}, &res)
	if res.Created != 1 || len(res.Missing) != 1 || res.Missing[0] != "Filme" {
		t.Fatalf("import: %+v", res)
	}
	c, _ := srvB.store.GetChannelByNumber(ctx, 7)
	cfg, _ := schedule.ParseConfig(c.Config)
	items, _ := srvB.lib.All(ctx)
	if got := cfg.Source.Rules.Filter(items); len(got) != 0 {
		t.Errorf("the channel takes %d titles of other libraries", len(got))
	}
}

// The import takes files as large as the preview does (logos travel inside).
func TestImportLargeFile(t *testing.T) {
	_, b := transferWorld(t, "b")
	file := exportFile{Format: exportFormat, Version: exportVersion, Channels: []exportChannel{{Name: "Groß", Number: 9, Kind: "smart",
		Config: json.RawMessage(`{"source":{"type":"rules"}}`), Logo: base64.StdEncoding.EncodeToString(make([]byte, 5<<20))}}}
	raw, _ := json.Marshal(file)
	var res struct{ Created int }
	if code := call(t, b, "POST", "/api/import", map[string]any{"file": json.RawMessage(raw), "choices": []importChoice{{Index: 0, Action: "new"}}}, &res); code != http.StatusOK || res.Created != 1 {
		t.Fatalf("import of a %d MB file: %d %+v", len(raw)>>20, code, res)
	}
}

// Exports from before the rename still import.
func TestImportOldFormat(t *testing.T) {
	_, b := transferWorld(t, "b")
	file := exportFile{Format: oldExportFormat, Version: exportVersion, Channels: []exportChannel{{Name: "Alt", Number: 9, Kind: "smart",
		Config: json.RawMessage(`{"source":{"type":"rules"}}`)}}}
	var preview struct {
		Channels []importPreview `json:"channels"`
	}
	if code := call(t, b, "POST", "/api/import/preview", file, &preview); code != http.StatusOK || len(preview.Channels) != 1 {
		t.Fatalf("old export: %d %+v", code, preview)
	}
}
