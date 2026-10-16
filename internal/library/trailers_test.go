package library

import (
	"encoding/json"
	"testing"

	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// Shapes as returned by a Plex Media Server (library extras) and by the Plex
// metadata provider (Discover extras), trimmed to the fields we use.
const libraryExtras = `[
 {"ratingKey":"291301","type":"clip","subtype":"trailer","title":"Insidious (German Trailer 1)","duration":156000,
  "Media":[{"duration":156000,"height":1080,"videoResolution":"1080","Part":[{"key":"/services/iva/assets/978097/video.mp4?fmt=4&bitrate=5000",
    "Stream":[{"id":153933,"streamType":1,"index":0},{"id":153934,"streamType":2,"index":1,"languageCode":"deu"}]}]},
   {"duration":156000,"height":720,"Part":[{"key":"/services/iva/assets/978097/video.mp4?fmt=4&bitrate=2500"}]}]},
 {"ratingKey":"291302","type":"clip","subtype":"sceneOrSample","title":"Szene","duration":60000,
  "Media":[{"height":720,"Part":[{"key":"/services/iva/assets/1/video.mp4"}]}]}
]`

const discoverExtras = `[
 {"ratingKey":"6a5e50c6e2c6f4315c4ee0cf","guid":"plex://clip/x","Guid":[{"id":"iva://1"}],"studio":"Marvel","Studio":[{"tag":"Marvel"}],
  "rating":7.1,"Rating":[{"type":"audience","value":7.1}],"type":"clip","subtype":"trailer","title":"Avengers: Doomsday (German Trailer 1)",
  "Media":[{"duration":137000,"height":1080,"url":"https://video.example/video.mp4?publishedid=1",
   "Part":[{"key":"/library/metadata/x/extras/y/parts/hls.m3u8","Stream":[{"id":"6a5e50c6-6a60039558","streamType":2,"languageCode":"de"}]}]}]}
]`

func TestTrailersFromExtras(t *testing.T) {
	var lib []plex.Metadata
	if err := json.Unmarshal([]byte(libraryExtras), &lib); err != nil {
		t.Fatal(err)
	}
	m := &store.Media{RatingKey: "291300", GUID: "plex://movie/abc", Title: "Insidious", Year: 2026, Tags: store.Tags{Genre: []string{"Horror"}}}
	ts := libraryTrailers(m, plex.ItemsFrom(lib))
	if len(ts) != 1 {
		t.Fatalf("expected 1 trailer (scenes skipped), got %d", len(ts))
	}
	tr := ts[0]
	if tr.Key != "t:291301" || tr.Language != "deu" || tr.DurationMs != 156000 || tr.MediaKey != "291300" ||
		tr.Origin != store.OriginLibrary || tr.Title != "Insidious" || tr.Genres[0] != "Horror" {
		t.Fatalf("unexpected trailer: %+v", tr)
	}
	if tr.Part(720) != "/services/iva/assets/978097/video.mp4?fmt=4&bitrate=2500" {
		t.Fatalf("720p part: %s", tr.Part(720))
	}

	var disc []plex.Metadata
	if err := json.Unmarshal([]byte(discoverExtras), &disc); err != nil {
		t.Fatal(err)
	}
	d := TrailerFrom(&plex.ItemsFrom(disc)[0])
	if d == nil || d.Language != "de" || d.DurationMs != 137000 || d.Part(720) != "https://video.example/video.mp4?publishedid=1" {
		t.Fatalf("discover trailer: %+v", d)
	}
}

func TestLocalGenres(t *testing.T) {
	g := localGenres([]string{"Comedy", "Horror"})
	if len(g) != 3 || g[0] != "Komödie" || g[1] != "Horror" || g[2] != "Comedy" {
		t.Errorf("genres: %v", g)
	}
}
