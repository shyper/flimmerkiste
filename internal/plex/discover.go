package plex

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Plex's online services behind the "Discover" area of the apps. They are not
// officially documented; everything using them must cope with failures.
const (
	discoverBase = "https://discover.provider.plex.tv"
	metadataBase = "https://metadata.provider.plex.tv"
)

// Discover is a client for the Plex Discover lists and the Plex metadata
// catalog, signed in with a plex.tv account token.
type Discover struct {
	Token    string
	ClientID string
	Language string // e.g. "de": titles and trailers in this language if available
}

func (d *Discover) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	setHeaders(req, d.ClientID, d.Token)
	if d.Language != "" {
		req.Header.Set("X-Plex-Language", d.Language)
	}
	return doJSON(req, out)
}

// Hub is one list of the Discover home screen.
type Hub struct {
	ID    string `json:"hubIdentifier"` // e.g. "home.coming-soon"
	Title string `json:"title"`
	Key   string `json:"key"`
	Type  string `json:"type"` // movie, mixed, clip, directory …
}

// Hubs lists the Discover home lists of the account.
func (d *Discover) Hubs(ctx context.Context) ([]Hub, error) {
	var r struct {
		MediaContainer struct {
			Hub []Hub `json:"Hub"`
		} `json:"MediaContainer"`
	}
	if err := d.get(ctx, discoverBase+"/hubs/sections/home", &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Hub, nil
}

// HubItems returns the entries of a list (movies, shows, seasons or trailer clips).
func (d *Discover) HubItems(ctx context.Context, key string, count int) ([]Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	sep := "?"
	if strings.Contains(key, "?") {
		sep = "&"
	}
	if err := d.get(ctx, discoverBase+key+sep+"count="+strconv.Itoa(count), &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Metadata, nil
}

// Item returns catalog metadata of a movie, show or season by its id (the
// last part of plex://movie/<id>).
func (d *Discover) Item(ctx context.Context, id string) (*Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := d.get(ctx, metadataBase+"/library/metadata/"+url.PathEscape(id), &r); err != nil {
		return nil, err
	}
	if len(r.MediaContainer.Metadata) == 0 {
		return nil, &StatusError{Code: http.StatusNotFound, URL: id}
	}
	return &r.MediaContainer.Metadata[0], nil
}

// Extras lists the extras of a catalog item in the client's language; their
// media entries carry direct file URLs.
func (d *Discover) Extras(ctx context.Context, id string) ([]Metadata, error) {
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := d.get(ctx, metadataBase+"/library/metadata/"+url.PathEscape(id)+"/extras", &r); err != nil {
		return nil, err
	}
	return r.MediaContainer.Metadata, nil
}

// GUIDID returns the id part of plex://movie/<id> ("" for other guids).
func GUIDID(guid string) string {
	if !strings.HasPrefix(guid, "plex://") {
		return ""
	}
	return guid[strings.LastIndex(guid, "/")+1:]
}
