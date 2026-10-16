package server

// Records API answers of a server with a small fake Plex library, for the web
// snapshot tests (web/src/tests/pages.test.js):
//
//	FLIMMERKISTE_WEB_FIXTURES=1 go test -run TestWebFixtures ./internal/server/

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/store"
)

func TestWebFixtures(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_WEB_FIXTURES") != "1" {
		t.Skip("set FLIMMERKISTE_WEB_FIXTURES=1 to record")
	}
	berlin(t)
	films := []struct {
		title string
		year  int
		genre []string
		min   int
	}{
		{"Zurück in die Zukunft", 1985, []string{"Komödie", "Science-Fiction"}, 116},
		{"Ghostbusters", 1984, []string{"Komödie", "Fantasy"}, 105},
		{"Die Goonies", 1985, []string{"Abenteuer", "Familie"}, 114},
		{"Stirb langsam", 1988, []string{"Action", "Thriller"}, 132},
		{"Kevin – Allein zu Haus", 1990, []string{"Komödie", "Familie"}, 103},
		{"Der Grinch", 2000, []string{"Komödie", "Familie", "Fantasy"}, 104},
		{"Halloween", 1978, []string{"Horror"}, 91},
		{"Shining", 1980, []string{"Horror", "Drama"}, 146},
	}
	now := time.Now().Truncate(time.Minute)
	var movies []map[string]any
	for i, f := range films {
		var g []map[string]string
		for _, x := range f.genre {
			g = append(g, map[string]string{"tag": x})
		}
		key := fmt.Sprint(i + 1)
		movies = append(movies, map[string]any{"ratingKey": key, "type": "movie", "title": f.title, "year": f.year,
			"duration": f.min * 60000, "summary": "Ein Klassiker: " + f.title + ".", "thumb": "/library/metadata/" + key + "/thumb/1",
			"Genre": g, "contentRating": "de/12", "audienceRating": 7.5, "addedAt": now.Add(-time.Duration(i*5) * 24 * time.Hour).Unix(),
			"viewCount": i % 3, "Media": []map[string]any{{"duration": f.min * 60000, "Part": []map[string]any{{"key": "/library/parts/" + key}}}}})
	}
	var episodes []map[string]any
	for e := 1; e <= 6; e++ {
		key := fmt.Sprintf("21%02d", e)
		episodes = append(episodes, map[string]any{"ratingKey": key, "type": "episode", "title": fmt.Sprintf("Folge %d", e),
			"index": e, "parentIndex": 1, "grandparentRatingKey": "200", "grandparentTitle": "Die Simpsons",
			"duration": 22 * 60000, "year": 1990, "Media": []map[string]any{{"duration": 22 * 60000, "Part": []map[string]any{{"key": "/library/parts/" + key}}}}})
	}
	shows := []map[string]any{{"ratingKey": "200", "type": "show", "title": "Die Simpsons", "year": 1989, "Genre": []map[string]string{{"tag": "Animation"}}}}
	mux := http.NewServeMux()
	mc := func(w http.ResponseWriter, v map[string]any) {
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": v})
	}
	mux.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) { mc(w, map[string]any{"machineIdentifier": "demo"}) })
	mux.HandleFunc("/library/sections", func(w http.ResponseWriter, r *http.Request) {
		mc(w, map[string]any{"Directory": []map[string]any{
			{"key": "1", "title": "Filme", "type": "movie", "Location": []map[string]string{{"path": "/data/movies"}}},
			{"key": "2", "title": "Serien", "type": "show", "Location": []map[string]string{{"path": "/data/tv"}}}}})
	})
	mux.HandleFunc("/library/sections/{id}/all", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Container-Start") != "0" {
			mc(w, map[string]any{"Metadata": []any{}})
			return
		}
		switch r.PathValue("id") + "/" + r.URL.Query().Get("type") {
		case "1/1":
			mc(w, map[string]any{"Metadata": movies})
		case "2/2":
			mc(w, map[string]any{"Metadata": shows})
		case "2/4":
			mc(w, map[string]any{"Metadata": episodes})
		default:
			mc(w, map[string]any{"Metadata": []any{}})
		}
	})
	plex := httptest.NewServer(mux)
	defer plex.Close()

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.SetSettings(ctx, map[string]string{
		store.SetServerURL: plex.URL, store.SetServerToken: "tok", store.SetServerID: "demo",
		store.SetServerName: "Wohnzimmer-Server", store.SetOwnerID: "1", store.SetInstanceID: "fixtures", store.SetLanguage: "de",
	})
	u := &store.User{Provider: "plex", ExternalID: "1", Username: "Alex", Role: "admin"}
	st.UpsertUser(ctx, u)
	st.CreateSession(ctx, "fixture", u.ID, 24*time.Hour)
	v := &store.User{Provider: "plex", ExternalID: "2", Username: "Gast", Role: "viewer"}
	st.UpsertUser(ctx, v)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(config.Config{DataDir: dir, FFmpeg: "ffmpeg", Horizon: 24, BaseURL: "http://192.168.1.10:8089"}, st, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.lib.StartSync()
	for srv.lib.Status().Running {
		time.Sleep(20 * time.Millisecond)
	}
	mk := func(name string, number int, kind, cfgJSON string) {
		c := &store.Channel{Number: number, Name: name, Enabled: true, Kind: kind, Config: json.RawMessage(cfgJSON)}
		if err := st.CreateChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := srv.sched.Reset(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	mk("Komödien", 1, "smart", `{"order":"shuffle","source":{"type":"rules","rules":{"match":"all","rules":[{"field":"genre","op":"is","values":["Komödie"]}]}}}`)
	mk("Grusel TV", 2, "smart", `{"order":"shuffle","logoStyle":"horror","source":{"type":"rules","rules":{"match":"all","rules":[{"field":"genre","op":"is","values":["Horror"]}]}}}`)
	mk("Simpsons", 3, "manual", `{"order":"fixed","source":{"type":"manual","items":[{"key":"200","type":"show","title":"Die Simpsons"}]}}`)

	h := srv.Handler()
	get := func(path string) any {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "fixture"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var body any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return body
	}
	from := now.Truncate(30 * time.Minute).Add(-30 * time.Minute) // as the guide page asks
	out := map[string]any{
		"now":  now.UnixMilli(),
		"from": from.UnixMilli(),
		"api": map[string]any{
			"/api/auth/status":      get("/api/auth/status"),
			"/api/meta":             get("/api/meta"),
			"/api/channels":         get("/api/channels"),
			"/api/channels/1":       get("/api/channels/1"),
			"/api/sessions":         get("/api/sessions"),
			"/api/guide":            get(fmt.Sprintf("/api/guide?from=%d&hours=6", from.UnixMilli())),
			"/api/settings":         get("/api/settings"),
			"/api/users":            get("/api/users"),
			"/api/trailers":         get("/api/trailers"),
			"/api/libraries":        get("/api/libraries"),
			"/api/library/facets":   get("/api/library/facets"),
			"/api/presets/seasonal": get("/api/presets/seasonal"),
			"/api/library/status":   get("/api/library/status"),
		},
	}
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "web", "src", "tests", "fixtures", "api.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
