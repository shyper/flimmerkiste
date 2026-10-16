package ai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

// TestGoldenPrompt keeps the prompts as they are. After an intended change:
//
//	FLIMMERKISTE_GOLDEN_UPDATE=1 go test -run TestGoldenPrompt ./internal/ai/
func TestGoldenPrompt(t *testing.T) {
	for l, name := range map[i18n.Lang]string{i18n.DE: "prompt.txt", i18n.EN: "prompt_en.txt"} {
		goldenPrompt(t, l, name)
	}
}

func goldenPrompt(t *testing.T, l i18n.Lang, name string) {
	req := Request{
		Lang:  l,
		Theme: "Gemütlicher Sonntagnachmittag", Count: 3,
		Date:   "Sunday, 13. December 2026, 15:00 Uhr",
		Recent: []string{"Der Grinch (2000)"},
		Candidates: []Candidate{
			{ID: "1", Kind: "movie", Title: "Zurück in die Zukunft", Year: 1985, Genres: []string{"Abenteuer", "Komödie"},
				Collections: []string{"Zurück in die Zukunft Filmreihe"}, Summary: "Marty reist in die Vergangenheit."},
			{ID: "200", Kind: "show", Title: "Die Simpsons", Year: 1989, Genres: []string{"Animation"}},
		},
	}
	got := "SYSTEM\n" + systemPrompt(req) + "\n\nUSER\n" + userPrompt(req) + "\n"
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("FLIMMERKISTE_GOLDEN_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with FLIMMERKISTE_GOLDEN_UPDATE=1)", err)
	}
	if string(want) != got {
		t.Errorf("prompt differs from %s:\n%s", path, got)
	}
}
