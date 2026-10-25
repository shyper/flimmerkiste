package server

// End-to-end test against a fake Plex server. Needs ffmpeg and takes ~40s:
//
//	FLIMMERKISTE_E2E=1 go test -run TestE2E -v ./internal/server/

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/store"
)

// lockedBuffer collects log output written from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func TestE2E(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" || testing.Short() {
		t.Skip("set FLIMMERKISTE_E2E=1 (and leave out -short) to run")
	}
	dir := t.TempDir()

	// --- test media: two short movies (different codecs settings) and two episodes ---
	media := map[string]string{}
	for i, spec := range []struct {
		name, size string
		dur        int
	}{
		{"m1.mkv", "640x360", 12}, {"m2.mp4", "640x360", 9}, {"e1.mkv", "640x360", 8}, {"e2.mkv", "640x360", 8},
		{"tr.mp4", "640x360", 7},
	} {
		out := filepath.Join(dir, spec.name)
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
			fmt.Sprintf("testsrc2=size=%s:rate=25:duration=%d", spec.size, spec.dur),
			"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:duration=%d", 300+i*100, spec.dur),
			"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("make media: %v %s", err, b)
		}
		media[spec.name] = out
	}

	// --- fake Plex Media Server ---
	part := func(n string) []map[string]any {
		return []map[string]any{{"id": 1, "key": "/library/parts/" + n, "duration": 0}}
	}
	files := map[string]string{} // rating key -> file name in dir (Plex sees it under /plexdata)
	movie := func(key, title string, year int, genre string, file string, dur int) map[string]any {
		files[key] = file
		return map[string]any{"ratingKey": key, "guid": "plex://movie/m" + key, "type": "movie", "title": title, "year": year, "duration": dur * 1000,
			"summary": "Testfilm " + title, "thumb": "/library/metadata/" + key + "/thumb/1",
			"Genre": []map[string]string{{"tag": genre}},
			"Media": []map[string]any{{"duration": dur * 1000, "container": "mkv", "videoCodec": "h264", "audioCodec": "aac", "Part": part(file)}}}
	}
	episode := func(key string, idx int, file string) map[string]any {
		files[key] = file
		return map[string]any{"ratingKey": key, "type": "episode", "title": fmt.Sprintf("Folge %d", idx), "index": idx, "parentIndex": 1,
			"grandparentRatingKey": "100", "grandparentTitle": "Testserie", "duration": 8000,
			"Media": []map[string]any{{"duration": 8000, "Part": part(file)}}}
	}
	movies := []map[string]any{
		movie("1", "Zurück in die Testzukunft", 1985, "Komödie", "m1.mkv", 12),
		movie("2", "Stirb Testsam", 1988, "Action", "m2.mp4", 9),
	}
	episodes := []map[string]any{episode("101", 1, "e1.mkv"), episode("102", 2, "e2.mkv")}
	shows := []map[string]any{{"ratingKey": "100", "type": "show", "title": "Testserie", "year": 1990, "Genre": []map[string]string{{"tag": "Komödie"}}}}

	plexMux := http.NewServeMux()
	container := func(w http.ResponseWriter, v map[string]any) {
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": v})
	}
	plexMux.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) {
		container(w, map[string]any{"machineIdentifier": "fake-machine"})
	})
	plexMux.HandleFunc("/library/sections", func(w http.ResponseWriter, r *http.Request) {
		container(w, map[string]any{"Directory": []map[string]any{
			{"key": "1", "title": "Filme", "type": "movie", "Location": []map[string]string{{"path": "/plexdata"}}},
			{"key": "2", "title": "Serien", "type": "show"}}})
	})
	// Collections and labels are missing from the listing, as with a real Plex server.
	plexMux.HandleFunc("/library/sections/{id}/collections", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "1" {
			container(w, map[string]any{"Metadata": []map[string]any{{"ratingKey": "900", "title": "Testreihe", "childCount": 1}}})
			return
		}
		container(w, map[string]any{})
	})
	plexMux.HandleFunc("/library/collections/{key}/children", func(w http.ResponseWriter, r *http.Request) {
		container(w, map[string]any{"Metadata": []map[string]any{{"ratingKey": "1", "type": "movie"}}})
	})
	plexMux.HandleFunc("/library/sections/{id}/label", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "2" {
			container(w, map[string]any{"Directory": []map[string]string{{"key": "77", "title": "Lieblingsserie"}}})
			return
		}
		container(w, map[string]any{})
	})
	plexMux.HandleFunc("/library/sections/{id}/all", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Container-Start") != "0" {
			container(w, map[string]any{"Metadata": []any{}})
			return
		}
		if label := r.URL.Query().Get("label"); label != "" {
			if label == "77" && r.URL.Query().Get("type") == "2" {
				container(w, map[string]any{"Metadata": shows})
			} else {
				container(w, map[string]any{"Metadata": []any{}})
			}
			return
		}
		switch r.PathValue("id") + "/" + r.URL.Query().Get("type") {
		case "1/1":
			container(w, map[string]any{"Metadata": movies})
		case "2/2":
			container(w, map[string]any{"Metadata": shows})
		case "2/4":
			container(w, map[string]any{"Metadata": episodes})
		default:
			container(w, map[string]any{"Metadata": []any{}})
		}
	})
	plexMux.HandleFunc("/library/metadata/{key}", func(w http.ResponseWriter, r *http.Request) {
		file := ""
		if f := files[r.PathValue("key")]; f != "" {
			file = "/plexdata/" + f
		}
		container(w, map[string]any{"Metadata": []map[string]any{{"ratingKey": r.PathValue("key"),
			"Media": []map[string]any{{"Part": []map[string]any{{"key": "x", "file": file, "Stream": []map[string]any{
				{"streamType": 1, "index": 0, "codec": "h264"},
				{"streamType": 2, "index": 1, "codec": "aac", "languageCode": "deu", "selected": true},
			}}}}}}}})
	})
	// Online extras: movie 1 has a German trailer, movie 2 only an English one.
	trailer := func(key, lang string) map[string]any {
		return map[string]any{"ratingKey": key, "type": "clip", "subtype": "trailer", "title": "Trailer " + key, "duration": 7000,
			"Media": []map[string]any{{"duration": 7000, "height": 360, "Part": []map[string]any{{
				"key": "/services/iva/assets/" + key + "/video.mp4?fmt=4&bitrate=750", "Stream": []map[string]any{{"streamType": 2, "languageCode": lang}}}}}}}
	}
	plexMux.HandleFunc("/library/metadata/{key}/extras", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("key") {
		case "1":
			container(w, map[string]any{"Metadata": []any{trailer("501", "deu")}})
		case "2":
			container(w, map[string]any{"Metadata": []any{trailer("502", "eng")}})
		default:
			container(w, map[string]any{})
		}
	})
	var trailerHits atomic.Int32
	plexMux.HandleFunc("/services/iva/assets/{id}/video.mp4", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Token") != "tok" || r.URL.Query().Get("fmt") != "4" {
			http.Error(w, "unauthorized", 401)
			return
		}
		trailerHits.Add(1)
		http.ServeFile(w, r, media["tr.mp4"])
	})
	var failParts atomic.Int32  // answer this many file requests with a server error, like a busy Plex
	var wakeHits atomic.Int32   // early reads of an upcoming file (disk wake-up)
	var brokenFile atomic.Value // this file always fails, like the one Plex refused in production
	brokenFile.Store("")
	partHits := map[string]*atomic.Int32{}
	for name := range media {
		partHits[name] = &atomic.Int32{}
	}
	plexMux.HandleFunc("/library/parts/{file}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-65535" {
			wakeHits.Add(1)
		} else if c := partHits[r.PathValue("file")]; c != nil {
			c.Add(1) // playback, not the early wake-up read
		}
		if r.PathValue("file") == brokenFile.Load() {
			http.Error(w, "Datei nicht lesbar", http.StatusInternalServerError)
			return
		}
		if failParts.Load() > 0 {
			failParts.Add(-1)
			http.Error(w, "busy", http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("X-Plex-Token") != "tok" {
			http.Error(w, "unauthorized", 401)
			return
		}
		http.ServeFile(w, r, media[r.PathValue("file")])
	})
	plex := httptest.NewServer(plexMux)
	defer plex.Close()

	// --- Flimmerkiste ---
	cfg := config.Config{DataDir: filepath.Join(dir, "data"), FFmpeg: "ffmpeg", Horizon: 5}
	os.MkdirAll(cfg.DataDir, 0o755)
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st.SetSettings(ctx, map[string]string{
		store.SetServerURL: plex.URL, store.SetServerToken: "tok", store.SetServerID: "fake-machine",
		store.SetServerName: "Fake", store.SetOwnerID: "1", store.SetLanguage: "de",
	})
	// The log is also kept to check no Plex token leaks into it. No redacting
	// handler here: the code itself has to keep tokens out.
	var logged lockedBuffer
	log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, &logged), &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv, err := New(cfg, st, log, fstest.MapFS{"index.html": {Data: []byte("ok")}})
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{Provider: "plex", ExternalID: "1", Username: "owner", Role: "admin"}
	st.UpsertUser(ctx, u)
	st.CreateSession(ctx, "sess", u.ID, time.Hour)
	app := httptest.NewServer(srv.Handler())
	defer app.Close()

	call := func(method, path string, body any, out any) int {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, app.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "sess"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("%s %s: %v (%s)", method, path, err, data)
			}
		}
		if resp.StatusCode >= 300 {
			t.Logf("%s %s -> %d %s", method, path, resp.StatusCode, data)
		}
		return resp.StatusCode
	}

	// sync library
	call("POST", "/api/library/sync", nil, nil)
	for i := 0; i < 50 && srv.lib.Status().Running; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if s := srv.lib.Status(); s.Error != "" {
		t.Fatalf("sync: %s", s.Error)
	}
	items, _ := srv.lib.All(ctx)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if items[0].Type == "episode" && len(items[0].Tags.Genre) == 0 {
		t.Fatal("episodes should inherit show genres")
	}
	for _, m := range items {
		switch {
		case m.RatingKey == "1" && (len(m.Tags.Collection) != 1 || m.Tags.Collection[0] != "Testreihe"):
			t.Fatalf("collection missing on movie 1: %v", m.Tags.Collection)
		case m.Type == "episode" && (len(m.Tags.Label) != 1 || m.Tags.Label[0] != "Lieblingsserie"):
			t.Fatalf("episodes should inherit show labels: %v", m.Tags.Label)
		case m.RatingKey == "2" && len(m.Tags.Collection) != 0:
			t.Fatalf("movie 2 is in no collection: %v", m.Tags.Collection)
		}
	}
	var facets map[string][]struct {
		Value string
		Count int
	}
	call("GET", "/api/library/facets", nil, &facets)
	if len(facets["collection"]) != 1 || facets["collection"][0].Value != "Testreihe" || len(facets["label"]) != 1 {
		t.Fatalf("facets: collections %v, labels %v", facets["collection"], facets["label"])
	}

	// preview with nested rules
	var prev struct{ Count int }
	call("POST", "/api/preview", map[string]any{"source": map[string]any{"type": "rules", "rules": map[string]any{
		"match": "all", "rules": []map[string]any{{"field": "genre", "op": "is", "values": []string{"Komödie"}}},
		"groups": []map[string]any{{"match": "none", "rules": []map[string]any{{"field": "type", "op": "is", "values": []string{"episode"}}}}},
	}}}, &prev)
	if prev.Count != 1 {
		t.Fatalf("preview: expected 1 comedy movie, got %d", prev.Count)
	}

	// channels: manual in fixed order, smart shuffle, seasonal preset
	var ch1, ch2 struct{ ID int64 }
	if code := call("POST", "/api/channels", map[string]any{"name": "Reihenkanal", "number": 1, "kind": "manual", "config": map[string]any{
		"order": "fixed", "source": map[string]any{"type": "manual", "items": []map[string]any{
			{"key": "1", "type": "movie"}, {"key": "100", "type": "show"}, {"key": "2", "type": "movie"}}}}}, &ch1); code != 201 {
		t.Fatalf("create channel: %d", code)
	}
	call("POST", "/api/channels", map[string]any{"name": "Alles Gemischt", "number": 2, "kind": "smart", "config": map[string]any{
		"order": "shuffle", "keepEpisodeOrder": true, "source": map[string]any{"type": "rules", "rules": map[string]any{"match": "all"}}}}, &ch2)
	var preset json.RawMessage
	call("GET", "/api/presets/seasonal", nil, &preset)
	call("POST", "/api/channels", map[string]any{"name": "Jahreszeiten", "number": 3, "kind": "seasonal", "config": preset}, nil)
	// planning runs in the background: wait for the first six programs of
	// channel 1, the sixth one not marked as left over from an earlier plan
	// (slow under the race detector)
	// waitPlanned waits until the background planning of a new or changed
	// channel is done (it counts as planning from the answer of the request on).
	waitPlanned := func(id int64) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); srv.sched.Planning(id); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("channel %d still planning after 30s", id)
			}
		}
	}
	const oldPlan = "e2e: before the re-plan"
	planned := func() []store.Program {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			progs, _ := st.ProgramsBetween(ctx, ch1.ID, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			if len(progs) >= 6 && progs[5].Note != oldPlan {
				return progs
			}
			if time.Now().After(deadline) {
				t.Fatalf("channel 1 not planned: %d programs", len(progs))
			}
		}
	}

	// fixed order: 1, ep1, ep2, 2, 1, ...
	progs := planned()
	var order []string
	for _, p := range progs[:6] {
		order = append(order, p.RatingKey)
	}
	if strings.Join(order, ",") != "1,101,102,2,1,101" {
		t.Fatalf("fixed order wrong: %v", order)
	}
	for i := 1; i < len(progs); i++ {
		if progs[i].Start != progs[i-1].Stop {
			t.Fatalf("gap in schedule at %d", i)
		}
	}

	// re-plan all channels: fixed order continues unchanged. The programs of
	// the old plan are marked: SQLite hands out the ids of deleted rows again,
	// so new programs do not always have higher ids.
	if _, err := st.DB.ExecContext(ctx, `UPDATE programs SET note = ? WHERE channel_id = ?`, oldPlan, ch1.ID); err != nil {
		t.Fatal(err)
	}
	var replan struct{ Channels int }
	if code := call("POST", "/api/channels/regenerate", nil, &replan); code != 200 || replan.Channels != 3 {
		t.Fatalf("regenerate all: %d, %d channels", code, replan.Channels)
	}
	progs = planned()
	order = order[:0]
	for _, p := range progs[:6] {
		order = append(order, p.RatingKey)
	}
	if strings.Join(order, ",") != "1,101,102,2,1,101" {
		t.Fatalf("fixed order after re-plan: %v", order)
	}

	// planning problems can be dismissed
	srv.sched.Ensure(ctx, -1) // unknown channel -> error remembered under -1
	if srv.sched.LastError(-1) == nil {
		t.Fatal("expected an error for an unknown channel")
	}
	srv.sched.ClearError(-1)
	var chErr struct{ Error string }
	if code := call("DELETE", fmt.Sprintf("/api/channels/%d/error", ch1.ID), nil, nil); code != 200 {
		t.Fatalf("dismiss error: %d", code)
	}
	if call("GET", fmt.Sprintf("/api/channels/%d/error", ch1.ID), nil, &chErr); chErr.Error != "" || srv.sched.LastError(-1) != nil {
		t.Fatalf("error after dismiss: %q", chErr.Error)
	}

	// HDHomeRun + XMLTV
	var lineup []map[string]any
	call("GET", "/lineup.json", nil, &lineup)
	if len(lineup) != 3 || !strings.HasSuffix(lineup[0]["URL"].(string), "/stream/1") {
		t.Fatalf("lineup: %v", lineup)
	}
	if code := call("PUT", "/api/settings", map[string]any{"epgPrefix": "bad prefix!"}, nil); code != 400 {
		t.Fatalf("invalid prefix accepted: %d", code)
	}
	call("PUT", "/api/settings", map[string]any{"epgPrefix": "fk-"}, nil)
	resp, _ := http.Get(app.URL + "/epg.xml")
	epg, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var doc xmltvDoc
	if err := xml.Unmarshal(epg, &doc); err != nil {
		t.Fatalf("xmltv: %v", err)
	}
	if len(doc.Channels) != 3 || len(doc.Programmes) < 10 {
		t.Fatalf("xmltv: %d channels, %d programmes", len(doc.Channels), len(doc.Programmes))
	}
	if ch := doc.Channels[0]; ch.ID != "fk-1" || !strings.HasPrefix(doc.Programmes[0].Channel, "fk-") ||
		len(ch.Names) != 3 || ch.Names[1] != "fk-1 "+ch.Names[0] || ch.Names[2] != "fk-1" || ch.LCN != "fk-1" {
		t.Fatalf("prefix not applied: channel %+v, programme %q", ch, doc.Programmes[0].Channel)
	}
	resp, _ = http.Get(app.URL + "/logos/auto-1.png?v=x")
	logoData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.HasPrefix(logoData, []byte("\x89PNG")) {
		t.Fatalf("logo: %d", resp.StatusCode)
	}

	// Stream channel 1 for 18s: starts mid-program and crosses at least one item boundary.
	now, _ := st.ProgramAt(ctx, ch1.ID, time.Now())
	t.Logf("tuning in %.1fs into %s", time.Since(now.StartTime()).Seconds(), now.RatingKey)
	sctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(sctx, "GET", app.URL+"/stream/1", nil)
	sresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := io.ReadAll(sresp.Body)
	sresp.Body.Close()
	if len(ts) < 188*100 || ts[0] != 0x47 || ts[188] != 0x47 {
		t.Fatalf("stream is not MPEG-TS (%d bytes)", len(ts))
	}
	tsFile := filepath.Join(dir, "capture.ts")
	os.WriteFile(tsFile, ts, 0o644)
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_name", "-of", "compact", tsFile).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v %s", err, out)
	}
	t.Logf("captured %d KB: %s", len(ts)/1024, strings.ReplaceAll(string(out), "\n", " | "))
	if !strings.Contains(string(out), "h264") || !strings.Contains(string(out), "aac") {
		t.Fatal("expected h264 + aac in stream")
	}
	var dur float64
	for _, f := range strings.Split(string(out), "|") {
		fmt.Sscanf(f, "duration=%f", &dur)
	}
	// 18s of wall time + 10s buffer; anything far beyond means we run ahead of the schedule.
	if dur > 30 {
		t.Fatalf("stream ran ahead of real time: %.1fs content in 18s", dur)
	}
	// Timestamps continue without gaps across item changes (a slow start only drains the buffer).
	out, err = exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", tsFile).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe packets: %v", err)
	}
	var prevPTS, maxGap float64
	for i, line := range strings.Fields(string(out)) {
		var pts float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(line, ","), "%f", &pts); err != nil {
			continue
		}
		if i > 0 && pts-prevPTS > maxGap {
			maxGap = pts - prevPTS
		}
		prevPTS = pts
	}
	if maxGap > 0.5 {
		t.Fatalf("timestamp gap of %.2fs between items", maxGap)
	}
	// --- aligned start times with filler clips and an "up next" slate ---
	var ch4 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Takt", "number": 4, "kind": "manual", "config": map[string]any{
		"order": "fixed", "align": 5, "fillerBetween": 1,
		"filler": map[string]any{"type": "manual", "items": []map[string]any{{"key": "100", "type": "show"}}},
		"source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "1", "type": "movie"}, {"key": "2", "type": "movie"}}}}}, &ch4)
	waitPlanned(ch4.ID)
	// A new channel with round start times begins at the last round time, as if it had been running.
	early, _ := st.ProgramsBetween(ctx, ch4.ID, time.Now().Add(-6*time.Minute), time.Now())
	if len(early) == 0 || early[0].RatingKey != "1" || early[0].Start > time.Now().UnixMilli() ||
		early[0].StartTime().Local().Sub(early[0].StartTime().Local().Truncate(5*time.Minute)) != 0 {
		t.Fatalf("first title should start at the last round time: %+v", early)
	}
	// Re-plan keeps the running program; the following ones must still start on round times.
	if err := srv.sched.Reset(ctx, ch4.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ProgramsBetween(ctx, ch4.ID, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	mains, fillers, cards, exact := 0, 0, 0, 0
	for i, p := range all {
		switch p.Kind {
		case "":
			mains++
			// Titles start on a round time, or directly when the previous one ended
			// less than a minute after it.
			off := p.StartTime().Local().Sub(p.StartTime().Local().Truncate(5 * time.Minute))
			if i > 0 && off >= time.Minute {
				t.Fatalf("main program %d not aligned: %s", i, p.StartTime().Local().Format("15:04:05.000"))
			}
			if off == 0 {
				exact++
			}
		case store.KindFiller:
			fillers++
		case store.KindCard:
			cards++
		}
		if i > 0 && p.Start != all[i-1].Stop {
			t.Fatalf("gap between program %d and %d", i-1, i)
		}
	}
	t.Logf("aligned channel: %d main (%d exactly on a round time), %d filler, %d cards in the next hour", mains, exact, fillers, cards)
	if mains < 10 || fillers < mains || cards == 0 || exact == 0 {
		t.Fatalf("unexpected filler structure")
	}
	// The guide hides fillers: main programs are extended up to the next start.
	var guide []struct {
		Channel  struct{ ID int64 }
		Programs []struct{ Start, Stop int64 }
	}
	call("GET", "/api/guide?hours=1", nil, &guide)
	for _, row := range guide {
		if row.Channel.ID != ch4.ID {
			continue
		}
		for i := 1; i < len(row.Programs); i++ {
			if row.Programs[i].Start != row.Programs[i-1].Stop {
				t.Fatal("guide still contains holes where fillers are")
			}
		}
	}

	// --- stream a slate, and a fully transcoded item with a burned-in logo ---
	capture := func(number int, dur time.Duration) string {
		sctx, cancel := context.WithTimeout(ctx, dur)
		defer cancel()
		req, _ := http.NewRequestWithContext(sctx, "GET", fmt.Sprintf("%s/stream/%d", app.URL, number), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		f := filepath.Join(dir, fmt.Sprintf("cap%d.ts", number))
		os.WriteFile(f, data, 0o644)
		out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width,height", "-of", "compact", f).CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe channel %d: %v %s", number, err, out)
		}
		return string(out)
	}
	var ch5 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Tafeltest", "number": 5, "kind": "manual", "config": map[string]any{
		"order": "fixed", "source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "2", "type": "movie"}}}}}, &ch5)
	waitPlanned(ch5.ID)
	now2 := time.Now()
	st.DeleteProgramsAfter(ctx, ch5.ID, now2.Add(-time.Hour))
	st.InsertPrograms(ctx, []store.Program{
		{ChannelID: ch5.ID, Start: now2.Add(-time.Second).UnixMilli(), Stop: now2.Add(6 * time.Second).UnixMilli(), RatingKey: store.CardKey, Kind: store.KindCard},
		{ChannelID: ch5.ID, Start: now2.Add(6 * time.Second).UnixMilli(), Stop: now2.Add(15 * time.Second).UnixMilli(), RatingKey: "2"},
	})
	if out := capture(5, 5*time.Second); !strings.Contains(out, "h264") || !strings.Contains(out, "width=1280") {
		t.Fatalf("card stream: %s", out)
	}
	cardFiles, _ := filepath.Glob(filepath.Join(cfg.DataDir, "cache", "card-*.png"))
	if len(cardFiles) == 0 {
		t.Fatal("card image was not rendered")
	}

	var ch6 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Logo im Bild", "number": 6, "kind": "manual", "config": map[string]any{
		"order": "fixed", "transcode": "full", "watermark": map[string]any{"enabled": true, "position": "br", "opacity": 60, "size": 15},
		"source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "1", "type": "movie"}}}}}, &ch6)
	waitPlanned(ch6.ID)
	if out := capture(6, 6*time.Second); !strings.Contains(out, "h264") || !strings.Contains(out, "width=1280") || !strings.Contains(out, "aac") {
		t.Fatalf("watermark stream: %s", out)
	}

	// --- trailers: found during the sync, as filler, as program previews and streamed ---
	for i := 0; i < 50; i++ {
		if ts, _ := st.Trailers(ctx); len(ts) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ts, _ := st.Trailers(ctx); len(ts) != 2 {
		t.Fatalf("expected 2 library trailers, got %d", len(ts))
	}
	var ch8 struct{ ID int64 }
	created8 := time.Now()
	call("POST", "/api/channels", map[string]any{"name": "Kino", "number": 8, "kind": "manual", "config": map[string]any{
		"order": "fixed", "align": 5, "previews": true, "filler": map[string]any{"type": "trailers"},
		"source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "1", "type": "movie"}, {"key": "2", "type": "movie"}}}}}, &ch8)
	waitPlanned(ch8.ID)
	var tprev struct{ Count, Trailers int }
	call("POST", "/api/preview", map[string]any{"source": map[string]any{"type": "trailers", "trailerOrigin": "library"}}, &tprev)
	if tprev.Count != 1 || tprev.Trailers != 1 {
		t.Fatalf("only the German trailer qualifies: %+v", tprev)
	}
	if err := srv.sched.BindPreviews(ctx, ch8.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// From the creation on: starting later, a slow run could begin inside a
	// promo whose trailer already ended.
	all8, _ := st.ProgramsBetween(ctx, ch8.ID, created8, time.Now().Add(time.Hour))
	trailerFillers, promos, unbound := 0, 0, 0
	for i, p := range all8 {
		switch {
		case p.Kind == store.KindFiller && p.RatingKey != "t:501":
			t.Fatalf("unexpected filler %s", p.RatingKey)
		case p.Kind == store.KindFiller:
			trailerFillers++
		case p.RatingKey == store.PromoKey:
			promos++
			target, err := st.ProgramAt(ctx, ch8.ID, time.UnixMilli(atoi64(p.Note)))
			if err != nil || target.RatingKey != "1" || i == 0 || all8[i-1].RatingKey != "t:501" {
				t.Fatalf("promo must follow the trailer and point at movie 1: %+v", p)
			}
		case p.Kind == store.KindPreview && p.Start < time.Now().Add(time.Hour).UnixMilli():
			unbound++
		}
		if i > 0 && p.Start != all8[i-1].Stop {
			t.Fatalf("gap between program %d and %d", i-1, i)
		}
	}
	t.Logf("trailer channel: %d trailer clips, %d previews in the next hour", trailerFillers, promos)
	if trailerFillers == 0 || promos == 0 || unbound != 0 {
		t.Fatalf("trailers: %d fillers, %d promos, %d unbound slots", trailerFillers, promos, unbound)
	}
	// Stream slate -> trailer -> movie: the trailer is prefetched while the slate plays.
	var ch9 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Trailerstream", "number": 9, "kind": "manual", "config": map[string]any{
		"order": "fixed", "source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "2", "type": "movie"}}}}}, &ch9)
	waitPlanned(ch9.ID)
	now3 := time.Now()
	st.DeleteProgramsAfter(ctx, ch9.ID, now3.Add(-time.Hour))
	st.InsertPrograms(ctx, []store.Program{
		{ChannelID: ch9.ID, Start: now3.Add(-time.Second).UnixMilli(), Stop: now3.Add(3 * time.Second).UnixMilli(), RatingKey: store.CardKey, Kind: store.KindCard},
		{ChannelID: ch9.ID, Start: now3.Add(3 * time.Second).UnixMilli(), Stop: now3.Add(10 * time.Second).UnixMilli(), RatingKey: "t:501", Kind: store.KindFiller},
		{ChannelID: ch9.ID, Start: now3.Add(10 * time.Second).UnixMilli(), Stop: now3.Add(19 * time.Second).UnixMilli(), RatingKey: "2"},
	})
	if out := capture(9, 8*time.Second); !strings.Contains(out, "h264") || !strings.Contains(out, "aac") {
		t.Fatalf("trailer stream: %s", out)
	}
	if trailerHits.Load() == 0 {
		t.Fatal("trailer was not fetched from Plex")
	}
	if wakeHits.Load() == 0 {
		t.Fatal("the movie after the trailer was not touched before it started")
	}
	time.Sleep(300 * time.Millisecond)
	if left, _ := filepath.Glob(filepath.Join(cfg.DataDir, "cache", "clip-*")); len(left) != 0 {
		t.Fatalf("prefetched clips not cleaned up: %v", left)
	}

	// Lower third over trailers: off by default; movie 1 does not air on this
	// channel, so the caption names another channel showing it.
	c9, _ := st.GetChannel(ctx, ch9.ID)
	trailerProg := &store.Program{ChannelID: ch9.ID, RatingKey: "t:501", Kind: store.KindFiller}
	if p, err := srv.renderCaption(ctx, c9, trailerProg, 720); err != nil || p != "" {
		t.Fatalf("caption while disabled: %q %v", p, err)
	}
	call("PUT", "/api/settings", map[string]any{"trailerCaption": true}, nil)
	t501, _ := st.GetTrailer(ctx, "t:501")
	if k := srv.captionKicker(ctx, c9, t501, time.Now()); !regexp.MustCompile(`^\S+ um \d\d:\d\d Uhr auf \S`).MatchString(k) {
		t.Fatalf("caption kicker: %q", k)
	}
	if p, err := srv.renderCaption(ctx, c9, trailerProg, 720); err != nil || !strings.Contains(p, "caption-") {
		t.Fatalf("caption: %q %v", p, err)
	} else if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/api/settings", map[string]any{"trailerCaption": false}, nil)
	call("PUT", "/api/settings", map[string]any{"cardMusic": false}, nil)
	var setOut map[string]any
	call("GET", "/api/settings", nil, &setOut)
	if setOut["cardMusic"] != false {
		t.Fatalf("cardMusic setting: got %v, want false", setOut["cardMusic"])
	}
	call("PUT", "/api/settings", map[string]any{"cardMusic": true}, nil)

	// A manual channel without titles plays only its trailers, and the guide shows
	// placeholder entries.
	var ch11 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Nur Trailer", "number": 11, "kind": "manual", "config": map[string]any{
		"order": "shuffle", "filler": map[string]any{"type": "trailers"}, "source": map[string]any{"type": "manual", "items": []any{}}}}, &ch11)
	waitPlanned(ch11.ID)
	if e := srv.sched.LastError(ch11.ID); e != nil {
		t.Fatalf("trailer-only channel: %s", e)
	}
	all11, _ := st.ProgramsBetween(ctx, ch11.ID, time.Now(), time.Now().Add(10*time.Minute))
	if len(all11) == 0 {
		t.Fatal("trailer-only channel has no programs")
	}
	for i, p := range all11 {
		if p.Kind != store.KindFiller || p.RatingKey != "t:501" {
			t.Fatalf("expected only the trailer, got %+v", p)
		}
		if i > 0 && p.Start != all11[i-1].Stop {
			t.Fatalf("gap between program %d and %d", i-1, i)
		}
	}
	resp, _ = http.Get(app.URL + "/epg.xml")
	epg11, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if n := strings.Count(string(epg11), "Keine Programminformationen verfügbar"); n < 2 {
		t.Fatalf("expected daily placeholder entries for channel 11, got %d", n)
	}
	var chans []struct {
		Number int
		Now    *struct{ Media struct{ Title string } }
	}
	call("GET", "/api/channels", nil, &chans)
	for _, c := range chans {
		if c.Number == 11 && (c.Now == nil || c.Now.Media.Title != "Keine Programminformationen verfügbar") {
			t.Fatalf("overview of the trailer-only channel: %+v", c.Now)
		}
	}
	if out := capture(11, 5*time.Second); !strings.Contains(out, "h264") {
		t.Fatalf("trailer-only stream: %s", out)
	}

	// A source Plex briefly cannot open is tried again instead of skipping the title.
	var ch10 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Wackelkandidat", "number": 10, "kind": "manual", "config": map[string]any{
		"order": "fixed", "source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "1", "type": "movie"}}}}}, &ch10)
	waitPlanned(ch10.ID)
	now4 := time.Now()
	st.DeleteProgramsAfter(ctx, ch10.ID, now4.Add(-time.Hour))
	st.InsertPrograms(ctx, []store.Program{
		{ChannelID: ch10.ID, Start: now4.Add(-time.Second).UnixMilli(), Stop: now4.Add(11 * time.Second).UnixMilli(), RatingKey: "1"},
		{ChannelID: ch10.ID, Start: now4.Add(11 * time.Second).UnixMilli(), Stop: now4.Add(30 * time.Second).UnixMilli(), RatingKey: store.CardKey, Kind: store.KindCard},
	})
	failParts.Store(2)
	if out := capture(10, 6*time.Second); !strings.Contains(out, "width=640") {
		t.Fatalf("movie was skipped after a temporary error (got only the slate): %s", out)
	}
	if failParts.Load() != 0 {
		t.Fatal("source was not requested again")
	}

	// A title that cannot be played keeps its slot: trailers fill it instead of the
	// next title starting early, so the channel stays in line with the guide.
	now5 := time.Now()
	st.DeleteProgramsAfter(ctx, ch10.ID, now5.Add(-time.Hour))
	st.InsertPrograms(ctx, []store.Program{
		{ChannelID: ch10.ID, Start: now5.Add(-time.Second).UnixMilli(), Stop: now5.Add(40 * time.Second).UnixMilli(), RatingKey: "1"},
		{ChannelID: ch10.ID, Start: now5.Add(40 * time.Second).UnixMilli(), Stop: now5.Add(49 * time.Second).UnixMilli(), RatingKey: "2"},
	})
	brokenFile.Store("m1.mkv")
	trailersBefore, nextBefore := trailerHits.Load(), partHits["m2.mp4"].Load()
	capture(10, 12*time.Second)
	brokenFile.Store("")
	if trailerHits.Load() == trailersBefore {
		t.Fatal("the slot of the broken title was not filled with trailers")
	}
	if partHits["m2.mp4"].Load() != nextBefore {
		t.Fatal("the next title started early")
	}
	if !strings.Contains(logged.String(), "the server answers: HTTP 500 Datei nicht lesbar") {
		t.Fatal("the server's answer missing in the log")
	}
	// Without trailers in a wanted language the fault slate fills the slot.
	ch10full, _ := st.GetChannel(ctx, ch10.ID)
	st.SetSetting(ctx, store.SetAudioLanguages, "fra")
	sub := srv.sched.Substitute(ctx, ch10full, "1", now5, now5.Add(time.Minute))
	st.SetSetting(ctx, store.SetAudioLanguages, "deu,ger")
	if len(sub) != 1 || sub[0].RatingKey != store.FaultKey || sub[0].Note != "1" || sub[0].Stop-sub[0].Start != 60000 {
		t.Fatalf("expected the fault slate: %+v", sub)
	}
	if path, err := srv.renderCard(ctx, ch10full, &sub[0], 720); err != nil || path == "" {
		t.Fatalf("fault slate: %v", err)
	}

	// --- library changes: movie 2 is deleted, movie 3 and episode 103 are added ---
	var ch7 struct{ ID int64 }
	call("POST", "/api/channels", map[string]any{"name": "Nur Komödie", "number": 7, "kind": "smart", "config": map[string]any{
		"order": "shuffle", "noRepeatHours": 0, "source": map[string]any{"type": "rules", "rules": map[string]any{
			"match": "all", "rules": []map[string]any{{"field": "genre", "op": "is", "values": []string{"Komödie"}}}}}}}, &ch7)
	waitPlanned(ch7.ID)
	stableUntil := time.Now().Add(stableWindow)
	before7, _ := st.ProgramsBetween(ctx, ch7.ID, time.Now(), stableUntil)

	movies = []map[string]any{movies[0], movie("3", "Neue Testkomödie", 2026, "Komödie", "m1.mkv", 12)}
	episodes = append(episodes, episode("103", 3, "e1.mkv"))
	call("POST", "/api/library/sync", nil, nil)
	deadline := time.Now().Add(15 * time.Second)
	for {
		future, _ := st.ProgramsBetween(ctx, ch1.ID, time.Now(), time.Now().Add(3*time.Hour))
		gone := true
		for _, p := range future {
			if p.RatingKey == "2" {
				gone = false
			}
		}
		if gone || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	// Fixed order must continue seamlessly without the deleted movie and with the new episode.
	succ := map[string]string{"1": "101", "101": "102", "102": "103", "103": "1"}
	future1, _ := st.ProgramsBetween(ctx, ch1.ID, time.Now(), time.Now().Add(3*time.Hour))
	var prevKey string
	saw103 := false
	for _, p := range future1 {
		if !p.IsMain() {
			continue
		}
		if p.RatingKey == "2" {
			t.Fatal("deleted movie still scheduled")
		}
		if p.RatingKey == "103" {
			saw103 = true
		}
		if prevKey != "" && succ[prevKey] != p.RatingKey {
			t.Fatalf("fixed order broken after re-plan: %s followed by %s", prevKey, p.RatingKey)
		}
		prevKey = p.RatingKey
	}
	if !saw103 {
		t.Fatal("new episode of a manually added show was not scheduled")
	}
	// New titles only change the guide after the stable window.
	after7, _ := st.ProgramsBetween(ctx, ch7.ID, time.Now(), stableUntil)
	for i := range before7 {
		if before7[i].Start < time.Now().UnixMilli() || before7[i].Start >= stableUntil.Add(-time.Minute).UnixMilli() {
			continue
		}
		found := false
		for _, a := range after7 {
			if a.Start == before7[i].Start && a.RatingKey == before7[i].RatingKey {
				found = true
			}
		}
		if !found {
			t.Fatalf("program inside the stable window changed at %s", before7[i].StartTime().Format("15:04:05"))
		}
	}
	later7, _ := st.ProgramsBetween(ctx, ch7.ID, stableUntil, stableUntil.Add(2*time.Hour))
	saw3 := false
	for _, p := range later7 {
		if p.RatingKey == "3" {
			saw3 = true
		}
	}
	if !saw3 {
		t.Fatal("new matching movie was not added to the smart channel")
	}
	// The editor flags manual entries that no longer exist and gets their years for sorting.
	var view struct {
		Missing []string
		Config  struct {
			Source struct {
				Items []struct {
					Key, Type string
					Year      int
				}
			}
		}
	}
	call("GET", fmt.Sprintf("/api/channels/%d", ch1.ID), nil, &view)
	if len(view.Missing) != 1 || view.Missing[0] != "movie|2|0" {
		t.Fatalf("missing entries: %v", view.Missing)
	}
	if items := view.Config.Source.Items; len(items) != 3 || items[0].Year != 1985 || items[1].Year != 1990 || items[2].Year != 0 {
		t.Fatalf("years of manual entries: %+v", items)
	}
	t.Logf("library change handled: movie 2 removed, movie 3 + episode 103 scheduled, stable window kept")

	// --- media folders mounted in the container, assigned to Plex's library folders ---
	srv.container = true
	var libs struct {
		Container bool
		Libraries []struct {
			ID        string
			Locations []libraryLocation
		}
	}
	call("GET", "/api/libraries", nil, &libs)
	if !libs.Container || len(libs.Libraries) == 0 || len(libs.Libraries[0].Locations) != 1 || libs.Libraries[0].Locations[0].Plex != "/plexdata" {
		t.Fatalf("library folders: %+v", libs)
	}
	if code := call("PUT", "/api/libraries/1/paths", map[string]any{"paths": map[string]string{"/plexdata": "/nonexistent"}}, nil); code != 400 {
		t.Fatalf("missing folder accepted: %d", code)
	}
	if code := call("PUT", "/api/libraries/1/paths", map[string]any{"paths": map[string]string{"/elsewhere": dir}}, nil); code != 400 {
		t.Fatalf("folder of no library accepted: %d", code)
	}
	var check pathCheck
	call("PUT", "/api/libraries/1/paths", map[string]any{"paths": map[string]string{"/plexdata": dir}}, &check)
	if check.Checked == 0 || check.Found != check.Checked {
		t.Fatalf("sample files: %+v", check)
	}
	if got := srv.localFile(ctx, "/plexdata/m1.mkv"); got != filepath.Join(dir, "m1.mkv") {
		t.Fatalf("mapped file: %q", got)
	}
	call("PUT", "/api/libraries/1/paths", map[string]any{"paths": map[string]string{"/plexdata": ""}}, nil)
	if got := srv.localFile(ctx, "/plexdata/m1.mkv"); got != "" {
		t.Fatalf("assignment not removed: %q", got)
	}
	srv.container = false

	if strings.Contains(logged.String(), "X-Plex-Token=tok") {
		t.Fatal("Plex token written to the log")
	}

	if len(srv.streamer.Sessions()) != 0 {
		time.Sleep(500 * time.Millisecond)
		if len(srv.streamer.Sessions()) != 0 {
			t.Fatal("session not cleaned up after disconnect")
		}
	}
}
