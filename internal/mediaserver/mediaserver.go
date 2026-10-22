// Package mediaserver describes what Flimmerkiste needs from a media server (Plex,
// Jellyfin, Emby): the libraries and their items, playable files with their
// streams, artwork and theme music. Backends translate their API into these
// neutral types; features only some servers have are optional interfaces.
package mediaserver

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// ErrNotFound is returned (possibly wrapped) when the server has no such item,
// extra or theme.
var ErrNotFound = errors.New("not found on the media server")

// Kind selects the items of a library.
type Kind int

const (
	KindMovie Kind = iota + 1
	KindShow
	KindEpisode
)

// Library is a movie or show library of the server.
type Library struct {
	ID    string
	Title string
	Type  string   // movie, show (others are listed but not used)
	Paths []string // folders of the library as the server sees them
}

// Item is a movie, show, episode or extra.
type Item struct {
	ID        string
	GUID      string // the server's global id, e.g. plex://movie/… (optional)
	Type      string // movie, show, episode, clip …
	Subtype   string // extras: trailer, behindTheScenes …
	Title     string
	SortTitle string
	Year      int
	Duration  int64 // milliseconds
	Summary   string
	Thumb     string // artwork path for ImageURL, or an absolute URL
	Art       string
	Rating    float64 // critics or general rating, 0–10
	// AudienceRating is preferred over Rating when set.
	AudienceRating float64
	ContentRating  string // e.g. de/12, PG-13
	Studio         string
	AddedAt        int64  // unix seconds
	Released       string // YYYY-MM-DD
	ViewCount      int

	// Episodes: number in the season, season number and the show.
	Index       int
	ParentIndex int
	ShowID      string
	ShowTitle   string
	ShowThumb   string

	Genres      []string
	Actors      []string
	Directors   []string
	Writers     []string
	Countries   []string
	Collections []string
	Labels      []string

	Media   []MediaInfo
	Markers []Marker // only from Item, and only where the server detects them

	// ExternalIDs by provider: tmdb, imdb, tvdb (as far as the server knows them).
	ExternalIDs map[string]string
}

// MediaInfo is one version of an item.
type MediaInfo struct {
	Duration        int64
	Width           int
	Height          int
	AspectRatio     float64 // display aspect ratio, e.g. 1.33, 1.78, 2.35 (0 = unknown)
	URL             string  // direct file URL (online extras), optional
	Container       string
	VideoCodec      string
	AudioCodec      string
	VideoResolution string
	Parts           []Part
}

// Part is a file of a version.
type Part struct {
	Key      string // what StreamURL turns into a playable URL
	Duration int64
	File     string // path of the file as the server sees it
	Streams  []Stream
}

// Stream types.
const (
	StreamVideo    = 1
	StreamAudio    = 2
	StreamSubtitle = 3
)

// Stream is a video, audio or subtitle track of a part.
type Stream struct {
	ID           string
	StreamType   int // StreamVideo, StreamAudio, StreamSubtitle
	Index        int // index in the file (embedded tracks)
	Codec        string
	LanguageCode string
	Language     string
	Default      bool
	Selected     bool
	Forced       bool // subtitles only meant for foreign-language parts
	Channels     int
	Title        string // display title
	TrackTitle   string // name given in the file, e.g. "Forced"
	Key          string // external subtitle file, optional
	// ColorTransfer is the transfer characteristic of a video track as ffmpeg
	// names it: "smpte2084" (HDR10, PQ), "arib-std-b67" (HLG), "bt709" …
	ColorTransfer string
}

// HDR reports whether a video track is HDR (PQ or HLG) and needs tone mapping
// for an SDR picture.
func (s Stream) HDR() bool {
	switch strings.ToLower(s.ColorTransfer) {
	case "smpte2084", "arib-std-b67":
		return true
	}
	return false
}

// Marker is a section the server detected in a file: type "intro" or
// "credits", offsets in milliseconds. Final credits run to the end of the file.
type Marker struct {
	Type            string
	StartTimeOffset int64
	EndTimeOffset   int64
	Final           bool
}

// Server is a connection to the linked media server.
type Server interface {
	// Identity checks the server is reachable and returns its id.
	Identity(ctx context.Context) (string, error)
	Libraries(ctx context.Context) ([]Library, error)
	// Items pages through the movies, shows or episodes of a library, with
	// their collections and labels.
	Items(ctx context.Context, libraryID string, kind Kind, fn func([]Item) error) error
	// Item returns one item with its streams and markers.
	Item(ctx context.Context, id string) (*Item, error)
	// Extras lists trailers, featurettes … of an item.
	Extras(ctx context.Context, id string) ([]Item, error)
	// Theme returns the theme music of an item (ErrNotFound without one).
	Theme(ctx context.Context, id string) ([]byte, error)
	// StreamURL is the URL ffmpeg reads a part from (with range requests).
	StreamURL(partKey string) string
	// SubtitleURL is the URL of an external subtitle track ("" = none).
	SubtitleURL(s Stream) string
	// ImageURL returns artwork scaled to fit width × height.
	ImageURL(path string, width, height int) string
}

// GuideReloader is a server whose live TV guide can be reloaded on demand.
type GuideReloader interface {
	// ReloadGuide reloads the guide of the tuner announcing deviceID (or
	// reachable at baseURL) and returns how many guides were reloaded.
	ReloadGuide(ctx context.Context, deviceID, baseURL string) (int, error)
}

// LiveTVState tells whether our tuner and guide are set up on the server.
type LiveTVState struct {
	Tuner      bool `json:"tuner"`
	Guide      bool `json:"guide"`
	TunerAdded bool `json:"tunerAdded,omitempty"`
	GuideAdded bool `json:"guideAdded,omitempty"`
}

// LiveTVSetup is a server where Flimmerkiste can add itself as tuner and guide.
type LiveTVSetup interface {
	SetUpLiveTV(ctx context.Context, baseURL string) (LiveTVState, error)
	LiveTVStatus(ctx context.Context, baseURL string) (LiveTVState, error)
}

// CatalogList is a list of an online catalog, e.g. "Coming soon".
type CatalogList struct {
	ID    string `json:"hubIdentifier"`
	Title string `json:"title"`
	Key   string `json:"key"`
	Type  string `json:"type"` // movie, mixed, clip, directory …
}

// CatalogTitle is a movie or show advertised in a catalog list.
type CatalogTitle struct {
	ID    string // id for Details and Extras
	GUID  string // matches Item.GUID of the same title in a library
	Kind  string // movie, show
	Title string
}

// TrailerCatalog is an online catalog of upcoming and popular titles with
// trailers (Plex Discover).
type TrailerCatalog interface {
	Lists(ctx context.Context) ([]CatalogList, error)
	Titles(ctx context.Context, list CatalogList, count int) ([]CatalogTitle, error)
	Details(ctx context.Context, id string) (*Item, error)
	Extras(ctx context.Context, id string) ([]Item, error)
}

var secrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(X-Plex-Token=)[^&\s"'<>]+`),
	regexp.MustCompile(`(?i)(api_key=)[^&\s"'<>]+`),
	regexp.MustCompile(`(?i)(X-Emby-Token=)[^&\s"'<>]+`),
	regexp.MustCompile(`(?i)(Token=")[^"]+`),
	regexp.MustCompile(`(fk_)[0-9a-f]{64}`), // Flimmerkiste API tokens, e.g. in /mcp/<token>
}

// Redact hides access tokens in URLs inside s (error messages, ffmpeg output,
// command lines), so they never reach a log.
func Redact(s string) string {
	for _, re := range secrets {
		s = re.ReplaceAllString(s, "${1}***")
	}
	return s
}
