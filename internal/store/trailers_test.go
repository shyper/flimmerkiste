package store

import "testing"

func TestLanguageRank(t *testing.T) {
	prefs := []string{"deu", "ger", "eng"}
	cases := map[string]int{"deu": 0, "de": 0, "ger": 0, "eng": 2, "en": 2, "fr": -1, "": -1}
	for code, want := range cases {
		if got := LanguageRank(code, prefs); got != want {
			t.Errorf("%q: got %d, want %d", code, got, want)
		}
	}
	if LanguageRank("fr", nil) != 0 {
		t.Error("without preferences every language is fine")
	}
	if LanguageTag("deu") != "de" || LanguageTag("ger") != "de" || LanguageTag("eng") != "en" {
		t.Error("language tags")
	}
}

func TestTrailerPart(t *testing.T) {
	tr := &Trailer{Parts: []TrailerPart{{1080, "hd"}, {720, "md"}, {270, "sd"}}}
	cases := map[int]string{1080: "hd", 720: "md", 800: "md", 0: "sd", 100: "sd"}
	for h, want := range cases {
		if got := tr.Part(h); got != want {
			t.Errorf("height %d: got %s, want %s", h, got, want)
		}
	}
}
