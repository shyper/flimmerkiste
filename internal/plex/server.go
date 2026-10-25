package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Server is a client for one Plex Media Server.
type Server struct {
	BaseURL  string
	Token    string
	ClientID string
}

func (s *Server) get(ctx context.Context, path string, query url.Values, out any) error {
	u := strings.TrimRight(s.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	setHeaders(req, s.ClientID, s.Token)
	return doJSON(req, out)
}

// Identity checks the server is reachable and returns its machine identifier.
func (s *Server) Identity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var r struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/identity", nil, &r); err != nil {
		return "", err
	}
	if r.MediaContainer.MachineIdentifier == "" {
		return "", fmt.Errorf("plex: %s answers, but is no Plex server", s.BaseURL)
	}
	return r.MediaContainer.MachineIdentifier, nil
}

type Section struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Type     string `json:"type"` // movie, show, artist, photo
	Location []struct {
		Path string `json:"path"` // folder of the library as Plex sees it
	} `json:"Location"`
}

func (s *Server) Sections(ctx context.Context) ([]Section, error) {
	var r struct {
		MediaContainer struct {
			Directory []Section `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/library/sections", nil, &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Directory, nil
}

type Tag struct {
	Tag string `json:"tag"`
}

// Stream and Part ids are numbers on a Plex Media Server and strings in the Plex
// metadata catalog; FlexID takes both.
type Stream struct {
	ID           FlexID `json:"id"`
	StreamType   int    `json:"streamType"` // 1 video, 2 audio, 3 subtitle
	Index        int    `json:"index"`
	Codec        string `json:"codec"`
	LanguageCode string `json:"languageCode"`
	Language     string `json:"language"`
	Default      bool   `json:"default"`
	Selected     bool   `json:"selected"`
	Forced       bool   `json:"forced"` // subtitles only meant for foreign-language parts
	Channels     int    `json:"channels"`
	Title        string `json:"displayTitle"`
	TrackTitle   string `json:"title"`              // name given in the file, e.g. "Forced"
	Key          string `json:"key,omitempty"`      // external subtitle file ("/library/streams/…")
	ColorTrc     string `json:"colorTrc,omitempty"` // video: "smpte2084" (HDR10), "arib-std-b67" (HLG) …
}

// Marker is a section Plex detected in a file (needs Plex Pass): type "intro"
// or "credits", offsets in milliseconds. Final credits run to the end of the file.
type Marker struct {
	Type            string   `json:"type"`
	StartTimeOffset int64    `json:"startTimeOffset"`
	EndTimeOffset   int64    `json:"endTimeOffset"`
	Final           FlexBool `json:"final"`
}

// FlexBool is a flag that may come as true, 1 or "1".
type FlexBool bool

func (f *FlexBool) UnmarshalJSON(b []byte) error {
	v := strings.Trim(string(b), `"`)
	*f = v == "true" || v == "1"
	return nil
}

// FlexID is an id that may come as a JSON number or string.
type FlexID string

func (f *FlexID) UnmarshalJSON(b []byte) error {
	*f = FlexID(strings.Trim(string(b), `"`))
	if *f == "null" {
		*f = ""
	}
	return nil
}

type Part struct {
	Key      string   `json:"key"`
	Duration int64    `json:"duration"`
	File     string   `json:"file"`
	Stream   []Stream `json:"Stream"`
}

type MediaInfo struct {
	Duration        int64   `json:"duration"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	AspectRatio     float64 `json:"aspectRatio"` // display aspect ratio, e.g. 1.33, 1.78, 2.35
	URL             string  `json:"url"`         // direct file URL (Plex Discover extras)
	Container       string  `json:"container"`
	VideoCodec      string  `json:"videoCodec"`
	AudioCodec      string  `json:"audioCodec"`
	VideoResolution string  `json:"videoResolution"`
	Part            []Part  `json:"Part"`
}

// Metadata covers the fields we use from movies, shows and episodes.
type Metadata struct {
	RatingKey             string      `json:"ratingKey"`
	GUID                  string      `json:"guid"`
	PrimaryGUID           string      `json:"primaryGuid"` // Discover: the movie/show a clip belongs to
	Type                  string      `json:"type"`
	Subtype               string      `json:"subtype"` // extras: trailer, behindTheScenes, …
	Title                 string      `json:"title"`
	TitleSort             string      `json:"titleSort"`
	Year                  int         `json:"year"`
	Duration              int64       `json:"duration"`
	Summary               string      `json:"summary"`
	Thumb                 string      `json:"thumb"`
	Art                   string      `json:"art"`
	ContentRating         string      `json:"contentRating"`
	AudienceRating        float64     `json:"audienceRating"`
	Rating                float64     `json:"rating"`
	Studio                string      `json:"studio"`
	AddedAt               int64       `json:"addedAt"`
	OriginallyAvailableAt string      `json:"originallyAvailableAt"`
	ViewCount             int         `json:"viewCount"`
	Index                 int         `json:"index"`
	ParentIndex           int         `json:"parentIndex"`
	ParentRatingKey       string      `json:"parentRatingKey"`
	ParentGUID            string      `json:"parentGuid"`
	ParentTitle           string      `json:"parentTitle"`
	GrandparentRatingKey  string      `json:"grandparentRatingKey"`
	GrandparentTitle      string      `json:"grandparentTitle"`
	GrandparentThumb      string      `json:"grandparentThumb"`
	Genre                 []Tag       `json:"Genre"`
	Director              []Tag       `json:"Director"`
	Writer                []Tag       `json:"Writer"`
	Role                  []Tag       `json:"Role"`
	Country               []Tag       `json:"Country"`
	Collection            []Tag       `json:"Collection"`
	Label                 []Tag       `json:"Label"`
	Media                 []MediaInfo `json:"Media"`
	Marker                []Marker    `json:"Marker"` // only with includeMarkers (Item)

	// Plex also sends arrays named like the scalar fields above ("Guid" next to
	// "guid"). encoding/json matches names case-insensitively, so they need
	// fields of their own, or decoding fails.
	GUIDs   []struct{ ID string } `json:"Guid"`
	Studios []Tag                 `json:"Studio"`
	Ratings []json.RawMessage     `json:"Rating"`
}

// Plex item type numbers for /library/sections/{id}/all?type=N
const (
	TypeMovie   = 1
	TypeShow    = 2
	TypeEpisode = 4
)

// ListAll pages through all items of a type in a section and calls fn for each
// page. filter narrows the list (e.g. label=<id>), it may be nil.
func (s *Server) ListAll(ctx context.Context, section string, typ int, filter url.Values, fn func([]Metadata) error) error {
	const pageSize = 300
	first := "" // of the page before: a server ignoring the offset sends it again
	for start := 0; ; {
		q := url.Values{}
		for k, v := range filter {
			q[k] = v
		}
		q.Set("type", strconv.Itoa(typ))
		q.Set("includeGuids", "1")
		q.Set("X-Plex-Container-Start", strconv.Itoa(start))
		q.Set("X-Plex-Container-Size", strconv.Itoa(pageSize))
		var r struct {
			MediaContainer struct {
				TotalSize int        `json:"totalSize"`
				Size      int        `json:"size"`
				Metadata  []Metadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := s.get(ctx, "/library/sections/"+url.PathEscape(section)+"/all", q, &r); err != nil {
			return err
		}
		page := r.MediaContainer.Metadata
		if len(page) > 0 && start > 0 && page[0].RatingKey == first {
			return fmt.Errorf("plex: section %s returns the same page again", section)
		}
		if len(page) > 0 {
			first = page[0].RatingKey
			if err := fn(page); err != nil {
				return err
			}
		}
		total := r.MediaContainer.TotalSize
		if total > 0 && start+len(page) >= total || total == 0 && len(page) < pageSize {
			return nil
		}
		if len(page) == 0 {
			return fmt.Errorf("plex: section %s ends after %d of %d items", section, start, total)
		}
		start += len(page) // a short page: go on right after it
	}
}

// Collections lists the collections of a section.
func (s *Server) Collections(ctx context.Context, section string) ([]Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/library/sections/"+url.PathEscape(section)+"/collections", nil, &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Metadata, nil
}

// CollectionItems lists the movies or shows in a collection.
func (s *Server) CollectionItems(ctx context.Context, ratingKey string) ([]Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/library/collections/"+url.PathEscape(ratingKey)+"/children", nil, &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Metadata, nil
}

// Directory is an entry of a filter list such as the labels of a section.
type Directory struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Labels lists the labels used in a section.
func (s *Server) Labels(ctx context.Context, section string) ([]Directory, error) {
	var r struct {
		MediaContainer struct {
			Directory []Directory `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/library/sections/"+url.PathEscape(section)+"/label", nil, &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Directory, nil
}

// Item fetches full metadata (including stream details and markers) for one item.
func (s *Server) Item(ctx context.Context, ratingKey string) (*Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	q := url.Values{"includeMarkers": {"1"}, "includeGuids": {"1"}}
	if err := s.get(ctx, "/library/metadata/"+url.PathEscape(ratingKey), q, &r); err != nil {
		return nil, err
	}
	if len(r.MediaContainer.Metadata) == 0 {
		return nil, fmt.Errorf("plex: item %s not found", ratingKey)
	}
	return &r.MediaContainer.Metadata[0], nil
}

// Theme fetches the theme music of an item (MP3). Without one Plex answers 404
// (a *StatusError).
func (s *Server) Theme(ctx context.Context, ratingKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/library/metadata/"+url.PathEscape(ratingKey)+"/theme", nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, s.ClientID, s.Token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, &StatusError{Code: resp.StatusCode, URL: req.URL.Redacted()}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

// Extras lists the extras (trailers, featurettes …) of an item. Online extras
// come in the language of the library if available.
func (s *Server) Extras(ctx context.Context, ratingKey string) ([]Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/library/metadata/"+url.PathEscape(ratingKey)+"/extras", nil, &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Metadata, nil
}

// PartURL is the direct file URL ffmpeg reads from (supports HTTP range requests, so seeking is cheap).
// Keys of online extras already carry a query ("/services/iva/…?fmt=4&bitrate=…").
func (s *Server) PartURL(partKey string) string {
	sep := "?"
	if strings.Contains(partKey, "?") {
		sep = "&"
	}
	return strings.TrimRight(s.BaseURL, "/") + partKey + sep + "download=0&X-Plex-Token=" + url.QueryEscape(s.Token)
}

// ImageURL returns a resized image via Plex's photo transcoder.
func (s *Server) ImageURL(path string, width, height int) string {
	q := url.Values{}
	q.Set("width", strconv.Itoa(width))
	q.Set("height", strconv.Itoa(height))
	q.Set("minSize", "1")
	q.Set("upscale", "0")
	q.Set("url", path)
	q.Set("X-Plex-Token", s.Token)
	return strings.TrimRight(s.BaseURL, "/") + "/photo/:/transcode?" + q.Encode()
}

// DVR is a Live TV setup in Plex: one or more tuners plus their guide source.
type DVR struct {
	Key    flexString  `json:"key"`
	Lineup string      `json:"lineup"` // e.g. lineup://tv.plex.providers.epg.xmltv/http://host/epg.xml#…
	Device []DVRDevice `json:"Device"`
	// Lineups takes the "Lineup" child list; without it encoding/json would
	// match it case-insensitively to the "lineup" string above and fail.
	Lineups json.RawMessage `json:"Lineup"`
}

type DVRDevice struct {
	UUID             string `json:"uuid"`             // e.g. device://tv.plex.grabbers.hdhomerun/1A2B3C4D
	DeviceIdentifier string `json:"deviceIdentifier"` // the DeviceID from discover.json
	URI              string `json:"uri"`
}

// DVRs lists the Live TV DVRs (needs the server owner's token). Undocumented API.
func (s *Server) DVRs(ctx context.Context) ([]DVR, error) {
	var r struct {
		MediaContainer struct {
			Dvr []DVR `json:"Dvr"`
		} `json:"MediaContainer"`
	}
	if err := s.get(ctx, "/livetv/dvrs", nil, &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Dvr, nil
}

// ReloadGuide makes Plex fetch the guide of a DVR again (like "Refresh Guide"
// in the Plex settings). Plex reloads in the background. Undocumented API.
func (s *Server) ReloadGuide(ctx context.Context, dvrKey string) error {
	u := strings.TrimRight(s.BaseURL, "/") + "/livetv/dvrs/" + url.PathEscape(dvrKey) + "/reloadGuide"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	setHeaders(req, s.ClientID, s.Token)
	return doJSON(req, nil)
}

// flexString accepts a JSON string or number (Plex is not consistent with ids).
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case string:
		*f = flexString(v)
	case float64:
		*f = flexString(strconv.FormatFloat(v, 'f', -1, 64))
	}
	return nil
}
