package plex

import "testing"

func TestRedact(t *testing.T) {
	in := "Error opening input file http://h:32400/library/parts/1/file.mkv?download=0&X-Plex-Token=abc123.\n" +
		`-i "http://h/photo?x=1&x-plex-token=def&url=/a" -i http://h/b?X-Plex-Token=ghi`
	want := "Error opening input file http://h:32400/library/parts/1/file.mkv?download=0&X-Plex-Token=***\n" +
		`-i "http://h/photo?x=1&x-plex-token=***&url=/a" -i http://h/b?X-Plex-Token=***`
	if got := Redact(in); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
