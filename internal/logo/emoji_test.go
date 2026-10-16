package logo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHolidayForEmoji(t *testing.T) {
	cases := map[string]string{"🎃": "halloween", "❤️": "valentine", "🎄 ": "christmas", "🐣🌷": "easter", "⚽": "", "✨": "", "": ""}
	for in, want := range cases {
		if got := HolidayForEmoji(in); got != want {
			t.Errorf("HolidayForEmoji(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmojiImage(t *testing.T) {
	// plain, with presentation selector, ZWJ sequence, two emojis, no emoji
	for _, s := range []string{"⚽", "☀️", "🏳️‍🌈", "👩‍🚀", "🌸🌼"} {
		if emojiImage(s) == nil {
			t.Errorf("no Twemoji for %q", s)
		}
	}
	if emojiImage("ab") != nil {
		t.Error("letters should not be an emoji")
	}
}

func TestRenderSymbol(t *testing.T) {
	out := os.Getenv("LOGO_PREVIEW_DIR")
	cases := []struct{ name, style, sym string }{
		{"Saisonkino", "classic", "⚽"}, {"Saisonkino", "neon", "🌸"}, {"Sommer Kino", "ocean", "☀️"},
		{"Saisonkino", "horror", "🎃"}, {"Gruselnacht", "gothic", "✨"}, {"Kinderkanal", "kids", "🦖"},
	}
	for i, c := range cases {
		data, err := Render(Options{Name: c.name, Style: c.style, Symbol: c.sym, Number: 7})
		if err != nil {
			t.Fatal(err)
		}
		if out != "" {
			os.WriteFile(filepath.Join(out, "symbol-"+c.style+"-"+string(rune('a'+i))+".png"), data, 0o644)
		}
	}
}
