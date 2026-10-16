package jellyfin

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// The testdata holds real answers of Jellyfin 12.2 and Emby 4.10 for the test
// library (test/mediaserver), so the translation is checked without servers.
func TestItemFrom(t *testing.T) {
	for _, f := range []Flavor{Jellyfin, Emby} {
		t.Run(string(f), func(t *testing.T) {
			load := func(name string) mediaserver.Item {
				var b baseItem
				data, err := os.ReadFile("testdata/" + string(f) + "-" + name + ".json")
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &b); err != nil {
					t.Fatal(err)
				}
				c := &Client{Flavor: f, BaseURL: "http://media:8096", Token: "key"}
				return c.itemFrom(&b)
			}
			m := load("movie")
			if m.Type != "movie" || m.Title != "Matrix" || m.Year != 1999 || m.ContentRating != "de/16" ||
				m.ExternalIDs["tmdb"] != "603" || m.Released != "1999-03-31" || m.Rating < 8 || len(m.Labels) != 1 || m.Labels[0] != "Kult" ||
				len(m.Actors) == 0 || len(m.Directors) == 0 || m.Studio == "" {
				t.Errorf("movie: %+v", m)
			}
			if len(m.Media) == 0 || m.Media[0].Width != 640 || m.Media[0].AspectRatio < 1.7 || m.Media[0].VideoCodec != "h264" {
				t.Fatalf("media: %+v", m.Media)
			}
			var audio []string
			forced := false
			fileIdx := map[int]int{}
			for _, s := range m.Media[0].Parts[0].Streams {
				fileIdx[s.StreamType] = max(fileIdx[s.StreamType], s.Index)
				if s.StreamType == mediaserver.StreamVideo && s.Index != 0 {
					t.Errorf("video at file index %d", s.Index)
				}
				if s.StreamType == mediaserver.StreamAudio {
					audio = append(audio, s.LanguageCode)
				}
				forced = forced || s.StreamType == mediaserver.StreamSubtitle && s.Forced && s.Key != ""
			}
			var indexes []int
			for _, s := range m.Media[0].Parts[0].Streams {
				indexes = append(indexes, s.Index)
			}
			if len(audio) != 2 || audio[0] != "deu" || !forced || m.Media[0].Parts[0].Streams[0].StreamType == mediaserver.StreamVideo && indexes[0] != 0 {
				t.Errorf("indexes: %v", indexes)
			}
			if len(audio) != 2 || audio[0] != "deu" || !forced {
				t.Errorf("streams: %+v", m.Media[0].Parts[0].Streams)
			}
			e := load("episode")
			if e.Type != "episode" || e.ShowID == "" || e.ShowTitle != "Testserie" || e.ParentIndex == 0 || e.Index == 0 || e.ExternalIDs["tvdb"] == "" {
				t.Errorf("episode: %+v", e)
			}
		})
	}
}

func TestNormRating(t *testing.T) {
	for in, want := range map[string]string{"FSK-12": "de/12", "DE-16": "de/16", "fsk 6": "de/6", "PG-13": "PG-13", "": ""} {
		if got := normRating(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if a := parseAspect("2.35:1"); a < 2.34 || a > 2.36 {
		t.Error(a)
	}
}

func TestRedact(t *testing.T) {
	c := &Client{BaseURL: "http://m", Token: "secret"}
	if got := mediaserver.Redact(c.StreamURL("/Videos/1/stream?static=true")); got != "http://m/Videos/1/stream?static=true&api_key=***" {
		t.Error(got)
	}
}

// HDR videos carry their transfer characteristic for tone mapping.
func TestColorTransfer(t *testing.T) {
	for _, f := range []Flavor{Jellyfin, Emby} {
		data, err := os.ReadFile("testdata/" + string(f) + "-movie.json")
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		// mark the video track as HDR10, as the servers report it for such files
		for _, src := range raw["MediaSources"].([]any) {
			for _, st := range src.(map[string]any)["MediaStreams"].([]any) {
				if s := st.(map[string]any); s["Type"] == "Video" {
					s["ColorTransfer"] = "smpte2084"
				}
			}
		}
		data, _ = json.Marshal(raw)
		var b baseItem
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatal(err)
		}
		m := (&Client{Flavor: f, BaseURL: "http://media:8096", Token: "key"}).itemFrom(&b)
		hdr := false
		for _, s := range m.Media[0].Parts[0].Streams {
			hdr = hdr || s.StreamType == mediaserver.StreamVideo && s.HDR()
		}
		if !hdr {
			t.Errorf("%s: HDR video not recognised: %+v", f, m.Media[0].Parts[0].Streams)
		}
	}
}
