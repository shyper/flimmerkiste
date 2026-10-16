// Package news reads headlines from RSS and Atom feeds for the news slates.
// Only headlines, their source and time are kept: no teasers, no pictures.
package news

import (
	"cmp"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Headline is one news item.
type Headline struct {
	Title     string    `json:"title"`
	Source    string    `json:"source"` // name of the feed
	Published time.Time `json:"published"`
}

// StatusError is an HTTP error answer.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("feed: HTTP %d", e.Code) }

// feed covers RSS 2.0, RSS 1.0 (RDF) and Atom.
type feed struct {
	XMLName xml.Name
	// RSS 2.0
	Channel struct {
		Title string `xml:"title"`
		Items []item `xml:"item"`
	} `xml:"channel"`
	// RSS 1.0 items sit next to the channel
	RDFItems []item `xml:"item"`
	// Atom
	Title   string `xml:"title"`
	Entries []struct {
		Title     string `xml:"title"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
	} `xml:"entry"`
}

type item struct {
	Title   string `xml:"title"`
	PubDate string `xml:"pubDate"`
	Date    string `xml:"date"` // dc:date
}

// Fetch reads one feed.
func Fetch(ctx context.Context, url string) ([]Headline, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Flimmerkiste news slates")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, &StatusError{Code: resp.StatusCode}
	}
	return Parse(io.LimitReader(resp.Body, 4<<20), url)
}

// Parse reads a feed document; fallback names the source if the feed has no title.
func Parse(r io.Reader, fallback string) ([]Headline, error) {
	var f feed
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("feed: %w", err)
	}
	source := shortName(cmp.Or(clean(f.Channel.Title), clean(f.Title), fallback))
	var out []Headline
	add := func(title, date string) {
		if t := clean(title); t != "" {
			out = append(out, Headline{Title: t, Source: source, Published: parseDate(date)})
		}
	}
	for _, it := range append(f.Channel.Items, f.RDFItems...) {
		add(it.Title, cmp.Or(it.PubDate, it.Date))
	}
	for _, e := range f.Entries {
		add(e.Title, cmp.Or(e.Published, e.Updated))
	}
	if len(out) == 0 && f.XMLName.Local != "rss" && f.XMLName.Local != "feed" && f.XMLName.Local != "RDF" {
		return nil, fmt.Errorf("feed: not RSS or Atom (%s)", f.XMLName.Local)
	}
	return out, nil
}

// shortName drops the slogan from feed titles such as
// "tagesschau.de - die erste Adresse für Nachrichten und Information".
func shortName(s string) string {
	for _, sep := range []string{" - ", " – ", " — ", " | ", ": "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

var tags = regexp.MustCompile(`<[^>]*>`)

// clean drops markup and entities and folds whitespace.
func clean(s string) string {
	s = html.UnescapeString(tags.ReplaceAllString(s, ""))
	return strings.Join(strings.Fields(s), " ")
}

var dateLayouts = []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST", "2 Jan 2006 15:04:05 -0700", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05"}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Merge combines the headlines of several feeds: newest first, at most maxAge
// old (undated ones count as new), each title once.
func Merge(feeds [][]Headline, now time.Time, maxAge time.Duration) []Headline {
	var all []Headline
	seen := map[string]bool{}
	for _, f := range feeds {
		for i, h := range f {
			if !h.Published.IsZero() && now.Sub(h.Published) > maxAge {
				continue
			}
			key := strings.ToLower(h.Title)
			if seen[key] {
				continue
			}
			seen[key] = true
			if h.Published.IsZero() {
				// keep the feed's own order for undated items
				h.Published = now.Add(-time.Duration(i) * time.Minute)
			}
			all = append(all, h)
		}
	}
	slices.SortStableFunc(all, func(a, b Headline) int { return b.Published.Compare(a.Published) })
	return all
}
