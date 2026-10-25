package jellyfin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

var (
	_ mediaserver.Server        = (*Client)(nil)
	_ mediaserver.GuideReloader = (*Client)(nil)
)

// baseItem is the part of the API's item we use.
type baseItem struct {
	ID                  string                  `json:"Id"`
	Name                string                  `json:"Name"`
	SortName            string                  `json:"SortName"`
	Type                string                  `json:"Type"` // Movie, Series, Episode, BoxSet, Trailer, Audio …
	ProductionYear      int                     `json:"ProductionYear"`
	PremiereDate        string                  `json:"PremiereDate"`
	DateCreated         string                  `json:"DateCreated"`
	Overview            string                  `json:"Overview"`
	OfficialRating      string                  `json:"OfficialRating"`
	CommunityRating     float64                 `json:"CommunityRating"`
	CriticRating        float64                 `json:"CriticRating"`
	RunTimeTicks        int64                   `json:"RunTimeTicks"`
	Genres              []string                `json:"Genres"`
	Tags                []string                `json:"Tags"`
	TagItems            []struct{ Name string } `json:"TagItems"` // Emby
	ProductionLocations []string                `json:"ProductionLocations"`
	Studios             []struct{ Name string } `json:"Studios"`
	People              []struct {
		Name string `json:"Name"`
		Type string `json:"Type"` // Actor, Director, Writer …
	} `json:"People"`
	ProviderIDs           map[string]string `json:"ProviderIds"`
	SeriesID              string            `json:"SeriesId"`
	SeriesName            string            `json:"SeriesName"`
	IndexNumber           int               `json:"IndexNumber"`
	ParentIndexNumber     int               `json:"ParentIndexNumber"`
	Path                  string            `json:"Path"`
	ImageTags             map[string]string `json:"ImageTags"`
	BackdropImageTags     []string          `json:"BackdropImageTags"`
	SeriesPrimaryImageTag string            `json:"SeriesPrimaryImageTag"`
	UserData              struct {
		PlayCount int  `json:"PlayCount"`
		Played    bool `json:"Played"`
	} `json:"UserData"`
	MediaSources []mediaSource `json:"MediaSources"`
	Chapters     []chapter     `json:"Chapters"`
}

type chapter struct {
	StartPositionTicks int64  `json:"StartPositionTicks"`
	Name               string `json:"Name"`
	MarkerType         string `json:"MarkerType"` // Emby: IntroStart, IntroEnd, CreditsStart
}

type mediaSource struct {
	ID           string        `json:"Id"`
	Path         string        `json:"Path"`
	Container    string        `json:"Container"`
	RunTimeTicks int64         `json:"RunTimeTicks"`
	MediaStreams []mediaStream `json:"MediaStreams"`
}

type mediaStream struct {
	Type         string `json:"Type"` // Video, Audio, Subtitle
	Index        int    `json:"Index"`
	Codec        string `json:"Codec"`
	Language     string `json:"Language"`
	Title        string `json:"Title"`
	DisplayTitle string `json:"DisplayTitle"`
	IsDefault    bool   `json:"IsDefault"`
	IsForced     bool   `json:"IsForced"`
	IsExternal   bool   `json:"IsExternal"`
	Channels     int    `json:"Channels"`
	Width        int    `json:"Width"`
	Height       int    `json:"Height"`
	AspectRatio  string `json:"AspectRatio"` // "16:9", "2.35:1"
	// ColorTransfer: video, as ffprobe names it ("smpte2084" for HDR10, "arib-std-b67" for HLG)
	ColorTransfer string `json:"ColorTransfer"`
}

const itemFields = "Path,Genres,Studios,People,ProviderIds,Tags,DateCreated,PremiereDate,ProductionYear,Overview," +
	"OfficialRating,CommunityRating,CriticRating,SortName,ProductionLocations,MediaSources,MediaStreams,RunTimeTicks,TagItems"

func (c *Client) userQuery(q url.Values) url.Values {
	if q == nil {
		q = url.Values{}
	}
	if c.UserID != "" {
		q.Set("UserId", c.UserID)
	}
	return q
}

// Identity returns the server id.
func (c *Client) Identity(ctx context.Context) (string, error) {
	var info PublicInfo
	if err := c.get(ctx, "/System/Info/Public", nil, &info); err != nil {
		return "", err
	}
	if info.ID == "" {
		return "", fmt.Errorf("%s: %s answers, but sends no server id", c.Flavor, c.BaseURL)
	}
	return info.ID, nil
}

func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var r []struct {
		Name           string   `json:"Name"`
		ItemID         string   `json:"ItemId"`
		CollectionType string   `json:"CollectionType"`
		Locations      []string `json:"Locations"`
	}
	if err := c.get(ctx, "/Library/VirtualFolders", nil, &r); err != nil {
		return nil, err
	}
	out := make([]mediaserver.Library, 0, len(r))
	for _, l := range r {
		typ := l.CollectionType
		switch typ {
		case "movies":
			typ = "movie"
		case "tvshows":
			typ = "show"
		}
		out = append(out, mediaserver.Library{ID: l.ItemID, Title: l.Name, Type: typ, Paths: l.Locations})
	}
	return out, nil
}

func kindType(k mediaserver.Kind) string {
	switch k {
	case mediaserver.KindMovie:
		return "Movie"
	case mediaserver.KindShow:
		return "Series"
	case mediaserver.KindEpisode:
		return "Episode"
	}
	return ""
}

// items pages through /Items.
func (c *Client) items(ctx context.Context, q url.Values, fn func([]baseItem) error) error {
	const pageSize = 300
	q = c.userQuery(q)
	q.Set("Recursive", "true")
	q.Set("Fields", itemFields)
	first := "" // of the page before: a server ignoring the offset sends it again
	for start := 0; ; {
		q.Set("StartIndex", strconv.Itoa(start))
		q.Set("Limit", strconv.Itoa(pageSize))
		var r struct {
			Items            []baseItem `json:"Items"`
			TotalRecordCount int        `json:"TotalRecordCount"`
		}
		if err := c.get(ctx, "/Items", q, &r); err != nil {
			return err
		}
		if len(r.Items) > 0 && start > 0 && r.Items[0].ID == first {
			return fmt.Errorf("%s: the item list returns the same page again", c.Flavor)
		}
		if len(r.Items) > 0 {
			first = r.Items[0].ID
			if err := fn(r.Items); err != nil {
				return err
			}
		}
		total := r.TotalRecordCount
		if total > 0 && start+len(r.Items) >= total || total == 0 && len(r.Items) < pageSize {
			return nil
		}
		if len(r.Items) == 0 {
			return fmt.Errorf("%s: the item list ends after %d of %d items", c.Flavor, start, total)
		}
		start += len(r.Items) // a short page: go on right after it
	}
}

// Items lists the movies, shows or episodes of a library, with collections
// (box sets) for movies and shows.
func (c *Client) Items(ctx context.Context, libraryID string, kind mediaserver.Kind, fn func([]mediaserver.Item) error) error {
	if kindType(kind) == "" {
		return fmt.Errorf("%s: no item type for kind %v", c.Flavor, kind)
	}
	collections := map[string][]string{}
	if kind != mediaserver.KindEpisode {
		collections = c.boxSets(ctx)
	}
	q := url.Values{"ParentId": {libraryID}, "IncludeItemTypes": {kindType(kind)}}
	return c.items(ctx, q, func(page []baseItem) error {
		out := make([]mediaserver.Item, len(page))
		for i := range page {
			out[i] = c.itemFrom(&page[i])
			out[i].Collections = append(out[i].Collections, collections[page[i].ID]...)
		}
		return fn(out)
	})
}

// boxSets returns the collections per item id. Errors only cost the collections.
func (c *Client) boxSets(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	var sets []baseItem
	if err := c.items(ctx, url.Values{"IncludeItemTypes": {"BoxSet"}}, func(page []baseItem) error {
		sets = append(sets, page...)
		return nil
	}); err != nil {
		slog.Warn("load collections", "server", c.Flavor, "err", mediaserver.Redact(err.Error()))
	}
	for _, set := range sets {
		var r struct {
			Items []struct {
				ID string `json:"Id"`
			} `json:"Items"`
		}
		if err := c.get(ctx, "/Items", c.userQuery(url.Values{"ParentId": {set.ID}}), &r); err != nil {
			slog.Warn("load collection", "collection", set.Name, "err", mediaserver.Redact(err.Error()))
			continue
		}
		for _, it := range r.Items {
			out[it.ID] = append(out[it.ID], set.Name)
		}
	}
	return out
}

func (c *Client) Item(ctx context.Context, id string) (*mediaserver.Item, error) {
	var it baseItem
	path := "/Items/" + url.PathEscape(id)
	q := url.Values{"Fields": {itemFields + ",Chapters"}}
	if c.UserID != "" {
		path = "/Users/" + url.PathEscape(c.UserID) + "/Items/" + url.PathEscape(id)
	}
	if err := c.get(ctx, path, q, &it); err != nil {
		return nil, err
	}
	out := c.itemFrom(&it)
	out.Markers = chapterMarkers(it.Chapters, it.RunTimeTicks)
	return &out, nil
}

// chapterMarkers turns the intro and credits chapters Emby detects into
// markers (Jellyfin keeps them elsewhere and sends none).
func chapterMarkers(chapters []chapter, runTicks int64) []mediaserver.Marker {
	var out []mediaserver.Marker
	var introStart int64 = -1
	for _, ch := range chapters {
		ms := ch.StartPositionTicks / 10000
		switch ch.MarkerType {
		case "IntroStart":
			introStart = ms
		case "IntroEnd":
			if introStart >= 0 && ms > introStart {
				out = append(out, mediaserver.Marker{Type: "intro", StartTimeOffset: introStart, EndTimeOffset: ms})
			}
			introStart = -1
		case "CreditsStart":
			if end := runTicks / 10000; ms > 0 && (end == 0 || ms < end) {
				out = append(out, mediaserver.Marker{Type: "credits", StartTimeOffset: ms, EndTimeOffset: end, Final: true})
			}
		}
	}
	return out
}

// Extras lists the local trailers of an item.
func (c *Client) Extras(ctx context.Context, id string) ([]mediaserver.Item, error) {
	var r []baseItem
	path := "/Items/" + url.PathEscape(id) + "/LocalTrailers"
	if c.UserID != "" {
		path = "/Users/" + url.PathEscape(c.UserID) + "/Items/" + url.PathEscape(id) + "/LocalTrailers"
	}
	if err := c.get(ctx, path, url.Values{"Fields": {itemFields}}, &r); err != nil {
		return nil, err
	}
	out := make([]mediaserver.Item, 0, len(r))
	for i := range r {
		it := c.itemFrom(&r[i])
		it.Type, it.Subtype = "clip", "trailer"
		// the trailer's language is that of its first audio track
		out = append(out, it)
	}
	return out, nil
}

// Theme returns the first theme song of an item.
func (c *Client) Theme(ctx context.Context, id string) ([]byte, error) {
	var r struct {
		Items []struct {
			ID string `json:"Id"`
		} `json:"Items"`
	}
	if err := c.get(ctx, "/Items/"+url.PathEscape(id)+"/ThemeSongs", c.userQuery(nil), &r); err != nil {
		return nil, err
	}
	if len(r.Items) == 0 {
		return nil, mediaserver.ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/Audio/"+url.PathEscape(r.Items[0].ID)+"/stream?static=true", nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, &StatusError{Code: resp.StatusCode, URL: req.URL.Path}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

// partKey is what StreamURL turns into the file's URL.
func partKey(itemID, sourceID string) string {
	return "/Videos/" + url.PathEscape(itemID) + "/stream?static=true&MediaSourceId=" + url.QueryEscape(sourceID)
}

func (c *Client) withKey(u string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return c.base() + u + sep + "api_key=" + url.QueryEscape(c.Token)
}

func (c *Client) StreamURL(key string) string { return c.withKey(key) }

// SubtitleURL returns an external subtitle track converted to SRT. Key holds
// "itemID|sourceID|server index" for such tracks.
func (c *Client) SubtitleURL(s mediaserver.Stream) string {
	parts := strings.Split(s.Key, "|")
	if len(parts) != 3 {
		return ""
	}
	return c.withKey(fmt.Sprintf("/Videos/%s/%s/Subtitles/%s/Stream.srt", url.PathEscape(parts[0]), url.PathEscape(parts[1]), parts[2]))
}

// ImageURL takes artwork paths of the form "/Items/{id}/Images/Primary".
func (c *Client) ImageURL(path string, width, height int) string {
	q := url.Values{"maxWidth": {strconv.Itoa(width)}, "maxHeight": {strconv.Itoa(height)}, "quality": {"90"}}
	return c.withKey(path + "?" + q.Encode())
}

var ratingRE = regexp.MustCompile(`(?i)^(?:(?:fsk|de)[-\s]?(\d{1,2})|(0|6|12|16|18))$`)

// normRating writes German ratings the way Plex does ("FSK-12", "DE-12" → "de/12"),
// so filters written for Plex libraries keep working. A bare 0, 6, 12, 16 or 18
// (as some NFO scrapers write it) is taken as FSK too: no other common system
// uses exactly these steps.
func normRating(r string) string {
	if m := ratingRE.FindStringSubmatch(strings.TrimSpace(r)); m != nil {
		return "de/" + m[1] + m[2]
	}
	return r
}

func (c *Client) itemFrom(b *baseItem) mediaserver.Item {
	it := mediaserver.Item{
		ID: b.ID, Title: b.Name, SortTitle: b.SortName, Year: b.ProductionYear, Duration: b.RunTimeTicks / ticksPerMs,
		Summary: b.Overview, ContentRating: normRating(b.OfficialRating), AddedAt: parseUnix(b.DateCreated),
		Released: dateOnly(b.PremiereDate), Genres: b.Genres, Labels: b.Tags, Countries: b.ProductionLocations,
		Index: b.IndexNumber, ParentIndex: b.ParentIndexNumber, ShowID: b.SeriesID, ShowTitle: b.SeriesName,
	}
	// community rating (0–10) first, else the critics' (0–100)
	it.Rating = b.CommunityRating
	if it.Rating == 0 && b.CriticRating > 0 {
		it.Rating = b.CriticRating / 10
	}
	if b.UserData.Played {
		it.ViewCount = max(1, b.UserData.PlayCount)
	}
	switch b.Type {
	case "Movie":
		it.Type = "movie"
	case "Series":
		it.Type = "show"
	case "Episode":
		it.Type = "episode"
	case "Trailer":
		it.Type = "clip"
	default:
		it.Type = strings.ToLower(b.Type)
	}
	for _, t := range b.TagItems {
		it.Labels = append(it.Labels, t.Name)
	}
	if len(b.Studios) > 0 {
		it.Studio = b.Studios[0].Name
	}
	for _, p := range b.People {
		switch p.Type {
		case "Actor", "GuestStar":
			it.Actors = append(it.Actors, p.Name)
		case "Director":
			it.Directors = append(it.Directors, p.Name)
		case "Writer":
			it.Writers = append(it.Writers, p.Name)
		}
	}
	for k, v := range b.ProviderIDs {
		switch strings.ToLower(k) {
		case "tmdb", "imdb", "tvdb":
			if v != "" {
				if it.ExternalIDs == nil {
					it.ExternalIDs = map[string]string{}
				}
				it.ExternalIDs[strings.ToLower(k)] = v
			}
		}
	}
	if _, ok := b.ImageTags["Primary"]; ok {
		it.Thumb = "/Items/" + b.ID + "/Images/Primary"
	}
	if len(b.BackdropImageTags) > 0 {
		it.Art = "/Items/" + b.ID + "/Images/Backdrop"
	}
	if b.SeriesID != "" && b.SeriesPrimaryImageTag != "" {
		it.ShowThumb = "/Items/" + b.SeriesID + "/Images/Primary"
	}
	for _, ms := range b.MediaSources {
		mi := mediaserver.MediaInfo{Duration: ms.RunTimeTicks / ticksPerMs, Container: ms.Container}
		part := mediaserver.Part{Key: partKey(b.ID, ms.ID), Duration: mi.Duration, File: ms.Path}
		// Index is the track's position in the file: Jellyfin counts external
		// subtitles first, Emby last, so embedded tracks are counted here.
		embedded := ms.MediaStreams[:0:0]
		for _, st := range ms.MediaStreams {
			if !st.IsExternal {
				embedded = append(embedded, st)
			}
		}
		sort.SliceStable(embedded, func(i, j int) bool { return embedded[i].Index < embedded[j].Index })
		fileIndex := map[int]int{}
		for i, st := range embedded {
			fileIndex[st.Index] = i
		}
		for _, st := range ms.MediaStreams {
			s := mediaserver.Stream{Index: fileIndex[st.Index], Codec: st.Codec, LanguageCode: st.Language, Default: st.IsDefault,
				Forced: st.IsForced, Channels: st.Channels, Title: st.DisplayTitle, TrackTitle: st.Title, ColorTransfer: st.ColorTransfer}
			switch st.Type {
			case "Video":
				s.StreamType = mediaserver.StreamVideo
				if mi.Width == 0 {
					mi.Width, mi.Height, mi.VideoCodec = st.Width, st.Height, st.Codec
					mi.AspectRatio = parseAspect(st.AspectRatio)
					mi.VideoResolution = resolution(st.Height)
				}
			case "Audio":
				s.StreamType = mediaserver.StreamAudio
				if mi.AudioCodec == "" {
					mi.AudioCodec = st.Codec
				}
			case "Subtitle":
				s.StreamType = mediaserver.StreamSubtitle
				if st.IsExternal {
					s.Index = -1
					s.Key = fmt.Sprintf("%s|%s|%d", b.ID, ms.ID, st.Index)
				}
			default:
				continue
			}
			part.Streams = append(part.Streams, s)
		}
		mi.Parts = []mediaserver.Part{part}
		it.Media = append(it.Media, mi)
	}
	if it.Duration == 0 && len(it.Media) > 0 {
		it.Duration = it.Media[0].Duration
	}
	return it
}

func parseAspect(s string) float64 {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return 0
	}
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil || y == 0 {
		return 0
	}
	return x / y
}

func resolution(h int) string {
	switch {
	case h >= 2000:
		return "4k"
	case h >= 1000:
		return "1080"
	case h >= 700:
		return "720"
	case h > 0:
		return "sd"
	}
	return ""
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return ""
}
