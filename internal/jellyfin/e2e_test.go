package jellyfin

// Integration tests against real Jellyfin and Emby servers with the test
// library of test/mediaserver (see its compose.yml):
//
//	test/mediaserver/make-media.sh && docker compose -f test/mediaserver/compose.yml -p flimmerkiste-test up -d
//	FLIMMERKISTE_MEDIASERVER_E2E=all go test -count=1 -v ./internal/jellyfin/
//
// FLIMMERKISTE_MEDIASERVER_E2E=jellyfin or =emby tests one of them. Live TV on Emby
// needs Emby Premiere; that part runs only with FLIMMERKISTE_EMBY_PREMIERE=1.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

const (
	testUser     = "admin"
	testPassword = "admin-pass"
	viewerUser   = "viewer"
	viewerPass   = "viewer-pass"
)

var testServers = map[Flavor]string{Jellyfin: "http://127.0.0.1:18096", Emby: "http://127.0.0.1:18097"}

func wanted(t *testing.T, f Flavor) bool {
	v := os.Getenv("FLIMMERKISTE_MEDIASERVER_E2E")
	return v == "all" || v == string(f)
}

// setUp runs the startup wizard and creates the libraries once, then waits
// until the library scan found the test titles.
func setUp(t *testing.T, f Flavor) *Client {
	t.Helper()
	ctx := context.Background()
	base := testServers[f]
	info, flavor, err := Public(ctx, base)
	if err != nil {
		t.Fatalf("%s not reachable at %s: %v (start test/mediaserver/compose.yml)", f, base, err)
	}
	if flavor != f {
		t.Fatalf("expected %s, found %s", f, flavor)
	}
	anon := &Client{Flavor: f, BaseURL: base, DeviceID: "flimmerkiste-test"}
	if !info.StartupWizardCompleted && f == Jellyfin || f == Emby && !loginWorks(ctx, f, base) {
		must(t, anon.do(ctx, http.MethodPost, "/Startup/Configuration", nil, map[string]string{"UICulture": "de-DE", "MetadataCountryCode": "DE", "PreferredMetadataLanguage": "de"}, nil))
		var u any
		must(t, anon.get(ctx, "/Startup/User", nil, &u))
		must(t, anon.do(ctx, http.MethodPost, "/Startup/User", nil, map[string]string{"Name": testUser, "Password": testPassword}, nil))
		if f == Jellyfin {
			must(t, anon.do(ctx, http.MethodPost, "/Startup/RemoteAccess", nil, map[string]bool{"EnableRemoteAccess": true}, nil))
		}
		must(t, anon.do(ctx, http.MethodPost, "/Startup/Complete", nil, nil, nil))
	}
	s, err := Login(ctx, f, base, "flimmerkiste-test", testUser, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Flavor: f, BaseURL: base, Token: s.Token, DeviceID: "flimmerkiste-test", UserID: s.UserID}
	libs, err := c.Libraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) == 0 {
		for _, l := range []struct{ name, typ, path string }{{"Filme", "movies", "/media/movies"}, {"Serien", "tvshows", "/media/shows"}} {
			q := url.Values{"name": {l.name}, "collectionType": {l.typ}, "paths": {l.path}, "refreshLibrary": {"true"}}
			body := map[string]any{"LibraryOptions": map[string]any{"EnableInternetProviders": false,
				"PathInfos": []map[string]string{{"Path": l.path}}}}
			if f == Emby {
				q = url.Values{"Name": {l.name}, "CollectionType": {l.typ}, "RefreshLibrary": {"true"}}
			} else {
				// Jellyfin takes the paths from the query; a null PathInfos is refused (12.2)
				delete(body["LibraryOptions"].(map[string]any), "PathInfos")
			}
			must(t, c.do(ctx, http.MethodPost, "/Library/VirtualFolders", q, body, nil))
		}
	}
	// a viewer account
	if users, err := c.Users(ctx); err == nil {
		found := false
		for _, u := range users {
			found = found || u.Name == viewerUser
		}
		if !found {
			var nu struct {
				ID string `json:"Id"`
			}
			must(t, c.do(ctx, http.MethodPost, "/Users/New", nil, map[string]string{"Name": viewerUser, "Password": viewerPass}, &nu))
			if f == Emby { // Emby sets the password separately
				must(t, c.do(ctx, http.MethodPost, "/Users/"+nu.ID+"/Password", nil, map[string]any{"NewPw": viewerPass, "CurrentPw": "", "ResetPassword": false}, nil))
			}
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var r struct {
			TotalRecordCount int `json:"TotalRecordCount"`
		}
		_ = c.get(ctx, "/Items", c.userQuery(url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Movie,Episode"}}), &r)
		if r.TotalRecordCount >= 7 {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("library scan found %d of 7 titles", r.TotalRecordCount)
		}
		time.Sleep(2 * time.Second)
	}
}

func loginWorks(ctx context.Context, f Flavor, base string) bool {
	_, err := Login(ctx, f, base, "flimmerkiste-test", testUser, testPassword)
	return err == nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestServers(t *testing.T) {
	for _, f := range []Flavor{Jellyfin, Emby} {
		t.Run(string(f), func(t *testing.T) {
			if !wanted(t, f) {
				t.Skip("set FLIMMERKISTE_MEDIASERVER_E2E=all (or " + string(f) + ") with the test servers running")
			}
			testServer(t, f)
		})
	}
}

func testServer(t *testing.T, f Flavor) {
	ctx := context.Background()
	c := setUp(t, f)

	// sign-in: admin, viewer, wrong password
	if s, err := Login(ctx, f, c.BaseURL, "flimmerkiste-test", testUser, testPassword); err != nil || !s.Admin || s.Token == "" {
		t.Fatalf("admin login: %+v %v", s, err)
	}
	if s, err := Login(ctx, f, c.BaseURL, "flimmerkiste-test", viewerUser, viewerPass); err != nil || s.Admin {
		t.Errorf("viewer login: %+v %v", s, err)
	}
	if _, err := Login(ctx, f, c.BaseURL, "flimmerkiste-test", testUser, "wrong"); err == nil {
		t.Error("wrong password accepted")
	}

	// a new sign-in of the same device ends its older sessions
	s, err := Login(ctx, f, c.BaseURL, "flimmerkiste-test", testUser, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	c.Token = s.Token

	// the API key replaces the session token
	key, err := c.CreateAPIKey(ctx, "Flimmerkiste test")
	if err != nil || key == "" {
		t.Fatalf("api key: %q %v", key, err)
	}
	again, _ := c.CreateAPIKey(ctx, "Flimmerkiste test")
	if again != key {
		t.Errorf("second key %q, want the first %q", again, key)
	}
	c = &Client{Flavor: f, BaseURL: c.BaseURL, Token: key, DeviceID: "flimmerkiste-test", UserID: c.UserID}

	libs, err := c.Libraries(ctx)
	if err != nil || len(libs) != 2 {
		t.Fatalf("libraries: %+v %v", libs, err)
	}
	var movies, shows string
	for _, l := range libs {
		switch l.Type {
		case "movie":
			movies = l.ID
		case "show":
			shows = l.ID
		}
		if len(l.Paths) == 0 {
			t.Errorf("library %s without paths", l.Title)
		}
	}

	byTitle := map[string]mediaserver.Item{}
	collect := func(lib string, k mediaserver.Kind) {
		must(t, c.Items(ctx, lib, k, func(page []mediaserver.Item) error {
			for _, it := range page {
				byTitle[it.Title] = it
			}
			return nil
		}))
	}
	collect(movies, mediaserver.KindMovie)
	collect(shows, mediaserver.KindShow)
	collect(shows, mediaserver.KindEpisode)

	m, ok := byTitle["Matrix"]
	if !ok {
		t.Fatalf("no Matrix: %v", keys(byTitle))
	}
	if m.Type != "movie" || m.Year != 1999 || m.ContentRating != "de/16" || m.ExternalIDs["tmdb"] != "603" || m.ExternalIDs["imdb"] != "tt0133093" ||
		m.Rating < 8 || m.Duration < 80_000 || !contains(m.Genres, "Science-Fiction") || !contains(m.Labels, "Kult") ||
		len(m.Media) == 0 || m.Media[0].Parts[0].File == "" || m.Released != "1999-03-31" {
		t.Errorf("Matrix: %+v", m)
	}
	show := byTitle["Testserie"]
	if show.Type != "show" || show.ExternalIDs["tvdb"] != "71663" || show.ContentRating != "de/6" {
		t.Errorf("show: %+v", show)
	}
	ep := byTitle["Folge 2.1"]
	if ep.Type != "episode" || ep.ShowID != show.ID || ep.ParentIndex != 2 || ep.Index != 1 || ep.ShowTitle != "Testserie" {
		t.Errorf("episode: %+v", ep)
	}

	// details: two audio tracks, the forced subtitles, the picture
	d, err := c.Item(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var langs []string
	var forced *mediaserver.Stream
	streams := d.Media[0].Parts[0].Streams
	for i, s := range streams {
		switch s.StreamType {
		case mediaserver.StreamAudio:
			langs = append(langs, s.LanguageCode)
		case mediaserver.StreamSubtitle:
			if s.Forced {
				forced = &streams[i]
			}
		}
	}
	if strings.Join(langs, ",") != "deu,eng" || d.Media[0].Width != 640 || d.Media[0].AspectRatio < 1.7 {
		t.Errorf("details: langs %v, media %+v", langs, d.Media[0])
	}
	if forced == nil || c.SubtitleURL(*forced) == "" {
		t.Errorf("forced subtitles: %+v", streams)
	} else if body := fetch(t, c.SubtitleURL(*forced)); !bytes.Contains(body, []byte("Erzwungener Untertitel")) {
		t.Errorf("subtitle file: %q", body)
	}
	wide, _ := c.Item(ctx, byTitle["Ghostbusters"].ID)
	if a := wide.Media[0].AspectRatio; a < 2.3 || a > 2.5 {
		t.Errorf("Ghostbusters aspect %v", a)
	}

	// the file plays with range requests
	req, _ := http.NewRequest(http.MethodGet, c.StreamURL(m.Media[0].Parts[0].Key), nil)
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("stream: HTTP %d", resp.StatusCode)
	}

	// theme song and local trailer
	if data, err := c.Theme(ctx, m.ID); err != nil || len(data) < 1000 {
		t.Errorf("theme: %d bytes, %v", len(data), err)
	}
	if _, err := c.Theme(ctx, byTitle["Casablanca"].ID); !errors.Is(err, mediaserver.ErrNotFound) {
		t.Errorf("theme without one: %v", err)
	}
	extras, err := c.Extras(ctx, m.ID)
	if err != nil || len(extras) != 1 || extras[0].Subtype != "trailer" || len(extras[0].Media) == 0 {
		t.Errorf("trailers: %+v %v", extras, err)
	}

	// live TV
	if f == Emby && os.Getenv("FLIMMERKISTE_EMBY_PREMIERE") != "1" {
		t.Log("Emby live TV skipped: needs Emby Premiere (FLIMMERKISTE_EMBY_PREMIERE=1)")
		return
	}
	tuner := fakeTuner(t)
	st, err := c.SetUpLiveTV(ctx, tuner)
	if err != nil || !st.Tuner || !st.Guide {
		t.Fatalf("live TV setup: %+v %v", st, err)
	}
	// leave no tuner behind that stops answering after the test
	t.Cleanup(func() { removeLiveTV(c, tuner) })
	st, err = c.SetUpLiveTV(ctx, tuner)
	if err != nil || st.TunerAdded || st.GuideAdded {
		t.Errorf("second setup added again: %+v %v", st, err)
	}
	if n, err := c.ReloadGuide(ctx, "", tuner); err != nil || n != 1 {
		t.Errorf("guide reload: %d %v", n, err)
	}
}

// fakeTuner answers like an HDHomeRun with one channel, reachable from the
// containers as host.docker.internal.
func fakeTuner(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://host.docker.internal:%d", ln.Addr().(*net.TCPAddr).Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/discover.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"FriendlyName":"Flimmerkiste","Manufacturer":"Silicondust","ModelNumber":"HDTC-2US","FirmwareName":"hdhomeruntc_atsc","FirmwareVersion":"20200101","DeviceID":"1234ABCD","DeviceAuth":"x","TunerCount":2,"BaseURL":%q,"LineupURL":%q}`, base, base+"/lineup.json")
	})
	mux.HandleFunc("/lineup.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"GuideNumber":"1","GuideName":"Testsender","URL":%q}]`, base+"/stream/1")
	})
	mux.HandleFunc("/lineup_status.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ScanInProgress":0,"ScanPossible":1,"Source":"Cable","SourceList":["Cable"]}`)
	})
	mux.HandleFunc("/epg.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><tv><channel id="1"><display-name>Testsender</display-name></channel></tv>`)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return base
}

// removeLiveTV deletes the tuner and guide added for baseURL.
func removeLiveTV(c *Client, baseURL string) {
	ctx := context.Background()
	var opts liveTVOptions
	if c.get(ctx, "/System/Configuration/livetv", nil, &opts) != nil {
		return
	}
	for _, p := range opts.ListingProviders {
		if strings.HasPrefix(p.Path, baseURL) {
			_ = c.do(ctx, http.MethodDelete, "/LiveTv/ListingProviders", url.Values{"id": {p.ID}}, nil, nil)
		}
	}
	for _, h := range opts.TunerHosts {
		if sameURL(h.URL, baseURL) {
			_ = c.do(ctx, http.MethodDelete, "/LiveTv/TunerHosts", url.Values{"id": {h.ID}}, nil, nil)
		}
	}
}

func fetch(t *testing.T, u string) []byte {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func keys(m map[string]mediaserver.Item) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
