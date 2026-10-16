package news

import (
	"strings"
	"testing"
	"time"
)

const rss = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel>
<title>tagesschau.de - die erste Adresse für Nachrichten</title>
<item><title>Erste &amp; beste Meldung</title><pubDate>Fri, 16 Oct 2026 08:54:24 +0200</pubDate><dc:date>2026-10-16T06:54:24Z</dc:date></item>
<item><title><![CDATA[Zweite <b>Meldung</b>]]></title><pubDate>Fri, 16 Oct 2026 05:00:25 +0200</pubDate></item>
<item><title>Uralt</title><pubDate>Mon, 12 Oct 2026 05:00:25 +0200</pubDate></item>
</channel></rss>`

const atom = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>heise online News</title>
<entry><title>Atom-Meldung</title><updated>2026-10-16T08:42:00+02:00</updated><published>2026-10-16T08:40:00+02:00</published></entry>
<entry><title>erste &amp; beste meldung</title><updated>2026-10-16T07:00:00+02:00</updated></entry>
</feed>`

const rdf = `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/">
<channel><title>Alt-Feed</title></channel>
<item><title>Ohne Datum</title></item>
</rdf:RDF>`

func TestParse(t *testing.T) {
	r, err := Parse(strings.NewReader(rss), "x")
	if err != nil || len(r) != 3 {
		t.Fatalf("rss: %v %v", r, err)
	}
	if r[0].Source != "tagesschau.de" || r[0].Title != "Erste & beste Meldung" || r[0].Published.Unix() != 1792133664 {
		t.Errorf("rss item: %+v", r[0])
	}
	if r[1].Title != "Zweite Meldung" {
		t.Errorf("markup: %q", r[1].Title)
	}
	a, err := Parse(strings.NewReader(atom), "x")
	if err != nil || len(a) != 2 || a[0].Source != "heise online News" || a[0].Published.Format(time.RFC3339) != "2026-10-16T08:40:00+02:00" {
		t.Fatalf("atom: %+v %v", a, err)
	}
	d, err := Parse(strings.NewReader(rdf), "x")
	if err != nil || len(d) != 1 || d[0].Source != "Alt-Feed" || !d[0].Published.IsZero() {
		t.Fatalf("rdf: %+v %v", d, err)
	}
	if _, err := Parse(strings.NewReader("<html><body>Not a feed</body></html>"), "x"); err == nil {
		t.Error("html accepted")
	}

	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	m := Merge([][]Headline{r, a, d}, now, 36*time.Hour)
	var titles []string
	for _, h := range m {
		titles = append(titles, h.Title)
	}
	if got := strings.Join(titles, " | "); got != "Ohne Datum | Erste & beste Meldung | Atom-Meldung | Zweite Meldung" {
		t.Errorf("merged: %s", got)
	}
}
