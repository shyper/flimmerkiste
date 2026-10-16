package plex

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// Backend makes a Plex Media Server a mediaserver.Server (with the optional
// guide reload).
type Backend struct {
	*Server
	log *slog.Logger
}

var (
	_ mediaserver.Server         = (*Backend)(nil)
	_ mediaserver.GuideReloader  = (*Backend)(nil)
	_ mediaserver.TrailerCatalog = (*Catalog)(nil)
)

// NewBackend wraps a server; log takes warnings about partly missing data
// (nil = discard).
func NewBackend(s *Server, log *slog.Logger) *Backend {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Backend{Server: s, log: log}
}

// Is lets errors.Is(err, mediaserver.ErrNotFound) match a 404.
func (e *StatusError) Is(target error) bool {
	return target == mediaserver.ErrNotFound && e.Code == http.StatusNotFound
}

func (b *Backend) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	secs, err := b.Sections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]mediaserver.Library, 0, len(secs))
	for _, s := range secs {
		l := mediaserver.Library{ID: s.Key, Title: s.Title, Type: s.Type}
		for _, loc := range s.Location {
			l.Paths = append(l.Paths, loc.Path)
		}
		out = append(out, l)
	}
	return out, nil
}

func plexType(kind mediaserver.Kind) int {
	switch kind {
	case mediaserver.KindMovie:
		return TypeMovie
	case mediaserver.KindShow:
		return TypeShow
	}
	return TypeEpisode
}

// Items lists a library page by page. Plex leaves collections and labels out
// of the listing, so for movies and shows they come from their own lists.
func (b *Backend) Items(ctx context.Context, libraryID string, kind mediaserver.Kind, fn func([]mediaserver.Item) error) error {
	typ := plexType(kind)
	var collections, labels map[string][]Tag
	if kind != mediaserver.KindEpisode {
		collections, labels = b.sectionTags(ctx, libraryID, typ)
	}
	return b.ListAll(ctx, libraryID, typ, nil, func(page []Metadata) error {
		items := make([]mediaserver.Item, len(page))
		for i := range page {
			page[i].Collection = append(page[i].Collection, collections[page[i].RatingKey]...)
			page[i].Label = append(page[i].Label, labels[page[i].RatingKey]...)
			items[i] = ItemFrom(&page[i])
		}
		return fn(items)
	})
}

// sectionTags returns the collections and labels per movie or show. Errors
// only cost these tags, not the sync.
func (b *Backend) sectionTags(ctx context.Context, section string, typ int) (collections, labels map[string][]Tag) {
	collections, labels = map[string][]Tag{}, map[string][]Tag{}
	colls, err := b.Collections(ctx, section)
	if err != nil {
		b.log.Warn("load collections", "library", section, "err", err)
	}
	for _, c := range colls {
		items, err := b.CollectionItems(ctx, c.RatingKey)
		if err != nil {
			b.log.Warn("load collection", "collection", c.Title, "err", err)
			continue
		}
		for _, it := range items {
			collections[it.RatingKey] = append(collections[it.RatingKey], Tag{Tag: c.Title})
		}
	}
	dirs, err := b.Labels(ctx, section)
	if err != nil {
		b.log.Warn("load labels", "library", section, "err", err)
	}
	for _, d := range dirs {
		err := b.ListAll(ctx, section, typ, url.Values{"label": {d.Key}}, func(page []Metadata) error {
			for _, it := range page {
				labels[it.RatingKey] = append(labels[it.RatingKey], Tag{Tag: d.Title})
			}
			return nil
		})
		if err != nil {
			b.log.Warn("load label", "label", d.Title, "err", err)
		}
	}
	return collections, labels
}

func (b *Backend) Item(ctx context.Context, id string) (*mediaserver.Item, error) {
	md, err := b.Server.Item(ctx, id)
	if err != nil {
		return nil, err
	}
	it := ItemFrom(md)
	return &it, nil
}

func (b *Backend) Extras(ctx context.Context, id string) ([]mediaserver.Item, error) {
	extras, err := b.Server.Extras(ctx, id)
	if err != nil {
		return nil, err
	}
	return ItemsFrom(extras), nil
}

func (b *Backend) StreamURL(partKey string) string { return b.PartURL(partKey) }

// SubtitleURL returns the file of an external subtitle track, or the track
// extracted by Plex.
func (b *Backend) SubtitleURL(s mediaserver.Stream) string {
	key := s.Key
	if key == "" {
		if s.ID == "" {
			return ""
		}
		key = "/library/streams/" + s.ID
	}
	return b.PartURL(key)
}

// ReloadGuide reloads the guide of every DVR that uses our tuner.
func (b *Backend) ReloadGuide(ctx context.Context, deviceID, baseURL string) (int, error) {
	dvrs, err := b.DVRs(ctx)
	if err != nil {
		return 0, err
	}
	keys := OwnDVRs(dvrs, deviceID, baseURL)
	for _, k := range keys {
		if err := b.Server.ReloadGuide(ctx, k); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

// OwnDVRs picks the DVRs of our tuner: by the DeviceID it announces (stable
// across address changes), or else by the URL of our XMLTV guide.
func OwnDVRs(dvrs []DVR, deviceID, baseURL string) []string {
	var keys []string
	for _, d := range dvrs {
		if ownsDVR(d, deviceID, baseURL) {
			keys = append(keys, string(d.Key))
		}
	}
	return keys
}

func ownsDVR(d DVR, deviceID, baseURL string) bool {
	for _, dev := range d.Device {
		if strings.EqualFold(dev.DeviceIdentifier, deviceID) || strings.HasSuffix(strings.ToUpper(dev.UUID), "/"+strings.ToUpper(deviceID)) {
			return true
		}
	}
	if baseURL == "" {
		return false
	}
	lineup := d.Lineup
	if u, err := url.PathUnescape(lineup); err == nil {
		lineup = u
	}
	return strings.Contains(lineup, strings.TrimRight(baseURL, "/")+"/epg.xml")
}

func tags(in []Tag) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, t := range in {
		if s := strings.TrimSpace(t.Tag); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ItemFrom converts Plex metadata.
func ItemFrom(md *Metadata) mediaserver.Item {
	it := mediaserver.Item{
		ID: md.RatingKey, GUID: md.GUID, Type: md.Type, Subtype: md.Subtype, Title: md.Title, SortTitle: md.TitleSort,
		Year: md.Year, Duration: md.Duration, Summary: md.Summary, Thumb: md.Thumb, Art: md.Art,
		Rating: md.Rating, AudienceRating: md.AudienceRating, ContentRating: md.ContentRating, Studio: md.Studio,
		AddedAt: md.AddedAt, Released: md.OriginallyAvailableAt, ViewCount: md.ViewCount,
		Index: md.Index, ParentIndex: md.ParentIndex,
		ShowID: md.GrandparentRatingKey, ShowTitle: md.GrandparentTitle, ShowThumb: md.GrandparentThumb,
		Genres: tags(md.Genre), Actors: tags(md.Role), Directors: tags(md.Director), Writers: tags(md.Writer),
		Countries: tags(md.Country), Collections: tags(md.Collection), Labels: tags(md.Label),
	}
	for _, mi := range md.Media {
		m := mediaserver.MediaInfo{Duration: mi.Duration, Width: mi.Width, Height: mi.Height, AspectRatio: mi.AspectRatio,
			URL: mi.URL, Container: mi.Container, VideoCodec: mi.VideoCodec, AudioCodec: mi.AudioCodec, VideoResolution: mi.VideoResolution}
		for _, p := range mi.Part {
			part := mediaserver.Part{Key: p.Key, Duration: p.Duration, File: p.File}
			for _, s := range p.Stream {
				part.Streams = append(part.Streams, mediaserver.Stream{ID: string(s.ID), StreamType: s.StreamType, Index: s.Index,
					Codec: s.Codec, LanguageCode: s.LanguageCode, Language: s.Language, Default: s.Default, Selected: s.Selected,
					Forced: s.Forced, Channels: s.Channels, Title: s.Title, TrackTitle: s.TrackTitle, Key: s.Key,
					ColorTransfer: s.ColorTrc})
			}
			m.Parts = append(m.Parts, part)
		}
		it.Media = append(it.Media, m)
	}
	for _, g := range md.GUIDs {
		// tmdb://603, imdb://tt0133093, tvdb://169
		if p, id, ok := strings.Cut(g.ID, "://"); ok && id != "" && (p == "tmdb" || p == "imdb" || p == "tvdb") {
			if it.ExternalIDs == nil {
				it.ExternalIDs = map[string]string{}
			}
			it.ExternalIDs[p] = id
		}
	}
	for _, mk := range md.Marker {
		it.Markers = append(it.Markers, mediaserver.Marker{Type: mk.Type, StartTimeOffset: mk.StartTimeOffset,
			EndTimeOffset: mk.EndTimeOffset, Final: bool(mk.Final)})
	}
	return it
}

// ItemsFrom converts a list of Plex metadata.
func ItemsFrom(mds []Metadata) []mediaserver.Item {
	out := make([]mediaserver.Item, len(mds))
	for i := range mds {
		out[i] = ItemFrom(&mds[i])
	}
	return out
}

// Catalog makes Plex Discover a mediaserver.TrailerCatalog.
type Catalog struct {
	d *Discover
}

func NewCatalog(d *Discover) *Catalog { return &Catalog{d: d} }

func (c *Catalog) Lists(ctx context.Context) ([]mediaserver.CatalogList, error) {
	hubs, err := c.d.Hubs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]mediaserver.CatalogList, len(hubs))
	for i, h := range hubs {
		out[i] = mediaserver.CatalogList{ID: h.ID, Title: h.Title, Key: h.Key, Type: h.Type}
	}
	return out, nil
}

// Titles returns the movies and shows a list advertises.
func (c *Catalog) Titles(ctx context.Context, list mediaserver.CatalogList, count int) ([]mediaserver.CatalogTitle, error) {
	items, err := c.d.HubItems(ctx, list.Key, count)
	if err != nil {
		return nil, err
	}
	var out []mediaserver.CatalogTitle
	for i := range items {
		if t, ok := catalogTitle(&items[i]); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// catalogTitle maps a list entry to the title whose trailers we want.
func catalogTitle(m *Metadata) (mediaserver.CatalogTitle, bool) {
	t := mediaserver.CatalogTitle{GUID: m.GUID, Kind: m.Type, Title: m.Title}
	switch m.Type {
	case "movie", "show":
	case "season":
		// Season trailers are the current ones; the show is the advertised title.
		t.Kind, t.Title = "show", m.ParentTitle
	case "clip":
		// Trailer lists: the clip belongs to a movie or show (English clip, so look up ours).
		t.GUID = m.PrimaryGUID
		switch {
		case strings.HasPrefix(t.GUID, "plex://movie/") && len(t.GUID) > 13:
			t.Kind = "movie"
		case strings.HasPrefix(t.GUID, "plex://show/") && len(t.GUID) > 12:
			t.Kind = "show"
		default:
			return t, false
		}
	default:
		return t, false
	}
	if t.ID = GUIDID(t.GUID); t.ID == "" || t.Title == "" {
		return t, false
	}
	if m.Type == "season" && m.ParentGUID != "" {
		t.GUID = m.ParentGUID
	}
	return t, true
}

func (c *Catalog) Details(ctx context.Context, id string) (*mediaserver.Item, error) {
	md, err := c.d.Item(ctx, id)
	if err != nil {
		return nil, err
	}
	it := ItemFrom(md)
	return &it, nil
}

func (c *Catalog) Extras(ctx context.Context, id string) ([]mediaserver.Item, error) {
	extras, err := c.d.Extras(ctx, id)
	if err != nil {
		return nil, err
	}
	return ItemsFrom(extras), nil
}
