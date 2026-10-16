package i18n

import (
	"regexp"
	"sort"
	"testing"
	"time"
)

var verb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// TestCatalogs: every key exists in every language with the same placeholders.
func TestCatalogs(t *testing.T) {
	for l, c := range catalogs {
		for k, v := range en {
			w, ok := c[k]
			if !ok {
				t.Errorf("%s: missing %q", l, k)
				continue
			}
			a, b := verb.FindAllString(v, -1), verb.FindAllString(w, -1)
			sort.Strings(a)
			sort.Strings(b)
			if len(a) != len(b) {
				t.Errorf("%s %q: placeholders %v, English %v", l, k, b, a)
				continue
			}
			for i := range a {
				if a[i] != b[i] {
					t.Errorf("%s %q: placeholders %v, English %v", l, k, b, a)
					break
				}
			}
		}
		for k := range c {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: %q missing in English", l, k)
			}
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"de": DE, "de-AT": DE, "deu": DE, "en": EN, "en-US": EN, "fr": EN, "": EN} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %s", in, got)
		}
	}
	for in, want := range map[string]Lang{"de-DE,de;q=0.9,en;q=0.8": DE, "fr-FR,en;q=0.5": EN, "fr,de;q=0.5": DE, "": EN} {
		if got := FromAcceptLanguage(in); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %s", in, got)
		}
	}
}

func TestFormats(t *testing.T) {
	at := time.Date(2026, 12, 16, 20, 15, 0, 0, time.UTC)
	if Clock(DE, at) != "20:15" || Clock(EN, at) != "8:15 PM" {
		t.Error(Clock(DE, at), Clock(EN, at))
	}
	if Date(DE, at, false) != "16. Dezember" || Date(EN, at, true) != "December 16, 2026" {
		t.Error(Date(DE, at, false), Date(EN, at, true))
	}
	if got := T(DE, "air.weekday", Weekday(DE, at.Weekday()), Clock(DE, at)); got != "Mittwoch um 20:15 Uhr" {
		t.Error(got)
	}
	err := E("library.load", E("library.notLinked"))
	if Message(DE, err) != "Bibliotheken laden: kein Medienserver verknüpft" || err.Error() != "Loading libraries: no media server linked" {
		t.Error(Message(DE, err), err.Error())
	}
}
