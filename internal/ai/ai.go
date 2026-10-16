// Package ai lets a language model curate the next items of a channel from a
// theme ("A cozy Sunday afternoon", "Mind-bending movies", …).
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

// Candidate is one selectable title as presented to the model.
type Candidate struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"` // movie | show
	Title  string   `json:"title"`
	Year   int      `json:"year,omitempty"`
	Genres []string `json:"genres,omitempty"`
	// Collections the title belongs to in Plex ("Edgar Wallace Filmreihe"),
	// often the strongest hint for themes about series of films.
	Collections []string `json:"collections,omitempty"`
	Summary     string   `json:"summary,omitempty"`
}

type Pick struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type Request struct {
	Theme      string
	Count      int
	Candidates []Candidate
	Recent     []string  // titles aired recently, to avoid repetition
	Date       string    // human readable air date, gives the model seasonal context
	Lang       i18n.Lang // language of the prompt and of the reasons
}

type Provider interface {
	Pick(ctx context.Context, req Request) ([]Pick, error)
}

type Settings struct {
	Provider string // anthropic | openai
	APIKey   string
	Model    string
	BaseURL  string
}

var ErrDisabled = i18n.E("ai.disabled")

func New(s Settings) (Provider, error) {
	switch s.Provider {
	case "anthropic":
		if s.APIKey == "" {
			return nil, i18n.E("ai.noKey")
		}
		model := s.Model
		if model == "" {
			model = DefaultAnthropicModel
		}
		return newAnthropic(s.APIKey, model), nil
	case "openai":
		if s.BaseURL == "" {
			s.BaseURL = "https://api.openai.com/v1"
		}
		if s.Model == "" {
			return nil, i18n.E("ai.noModel")
		}
		return &openAI{baseURL: strings.TrimRight(s.BaseURL, "/"), key: s.APIKey, model: s.Model}, nil
	}
	return nil, ErrDisabled
}

// systemPrompts per language; the German one is the original.
var systemPrompts = map[i18n.Lang]string{
	i18n.DE: `Du bist Programmplaner eines privaten Fernsehsenders. Du stellst aus einer vorgegebenen Liste von Filmen und Serien ein stimmiges Programm zu einem Thema zusammen.

Regeln:
- Wähle ausschließlich Titel aus der Kandidatenliste und gib deren "id" exakt so zurück, wie sie dort steht.
- Wähle Titel, die wirklich zum Thema passen. Nutze dein Wissen über die Filme und Serien, nicht nur Titel und Genre.
- Achte auf einen schönen Programmfluss (Abwechslung, Steigerung, passende Reihenfolge – z. B. Teile einer Filmreihe in der richtigen Reihenfolge).
- Vermeide Titel, die kürzlich gelaufen sind, solange genug Alternativen existieren.
- Wenn weniger passende Titel existieren als gewünscht, gib weniger zurück.
- "reason" ist eine kurze, sympathische Begründung auf Deutsch (max. 15 Wörter), die im Programmführer angezeigt wird.`,
	i18n.EN: `You plan the programme of a private TV channel. From a given list of movies and shows you put together a coherent programme on a theme.

Rules:
- Pick only titles from the candidate list and return their "id" exactly as it is written there.
- Pick titles that really fit the theme. Use your knowledge of the movies and shows, not just title and genre.
- Mind a pleasant flow (variety, a build-up, a sensible order – e.g. parts of a film series in the right order).
- Avoid titles that aired recently as long as there are enough alternatives.
- If fewer fitting titles exist than requested, return fewer.
- "reason" is a short, friendly explanation in English (at most 15 words) shown in the guide.`,
}

func systemPrompt(req Request) string {
	if p, ok := systemPrompts[req.Lang]; ok {
		return p
	}
	return systemPrompts[i18n.EN]
}

var promptTexts = map[i18n.Lang]map[string]string{
	i18n.DE: {
		"theme": "Thema des Senders: %s\n", "date": "Sendetermin: %s\n", "count": "Gewünschte Anzahl: %d\n\n",
		"recent":     "Kürzlich gelaufen (möglichst nicht wiederholen):\n",
		"candidates": "Kandidaten (eine Zeile pro Titel: id | Art | Titel | Jahr | Genres | Sammlungen | Inhalt):\n",
		"movie":      "Film", "show": "Serie",
		"answer": "\nAntworte nur mit JSON der Form {\"picks\":[{\"id\":\"…\",\"reason\":\"…\"}]}.",
	},
	i18n.EN: {
		"theme": "Theme of the channel: %s\n", "date": "Air date: %s\n", "count": "Number wanted: %d\n\n",
		"recent":     "Aired recently (avoid repeating if possible):\n",
		"candidates": "Candidates (one line per title: id | type | title | year | genres | collections | summary):\n",
		"movie":      "Movie", "show": "Show",
		"answer": "\nAnswer only with JSON of the form {\"picks\":[{\"id\":\"…\",\"reason\":\"…\"}]}.",
	},
}

func userPrompt(req Request) string {
	t, ok := promptTexts[req.Lang]
	if !ok {
		t = promptTexts[i18n.EN]
	}
	var b strings.Builder
	fmt.Fprintf(&b, t["theme"], req.Theme)
	if req.Date != "" {
		fmt.Fprintf(&b, t["date"], req.Date)
	}
	fmt.Fprintf(&b, t["count"], req.Count)
	if len(req.Recent) > 0 {
		b.WriteString(t["recent"])
		for _, r := range req.Recent {
			b.WriteString("- " + r + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(t["candidates"])
	for _, c := range req.Candidates {
		kind := t["movie"]
		if c.Kind == "show" {
			kind = t["show"]
		}
		fmt.Fprintf(&b, "%s | %s | %s | %d | %s | %s | %s\n", c.ID, kind, c.Title, c.Year, strings.Join(c.Genres, ", "),
			strings.Join(c.Collections, ", "), c.Summary)
	}
	b.WriteString(t["answer"])
	return b.String()
}

type pickResponse struct {
	Picks []struct {
		// Library ids are numeric; models without structured output often
		// write them as numbers. Kept raw, so long ids keep every digit.
		ID     json.RawMessage `json:"id"`
		Title  string          `json:"title"` // some models answer with the title (too)
		Reason string          `json:"reason"`
	} `json:"picks"`
}

// rawID returns a JSON string or number as text ("" for anything else).
func rawID(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

var pickSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"picks": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string"},
					"reason": map[string]any{"type": "string"},
				},
				"required":             []string{"id", "reason"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"picks"},
	"additionalProperties": false,
}

// parsePicks decodes the model output and drops IDs that are not candidates.
func parsePicks(text string, req Request) ([]Pick, error) {
	text = strings.TrimSpace(text)
	// Tolerate code fences from models without structured output support.
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if i := strings.LastIndex(text, "}"); i >= 0 && i < len(text)-1 {
		text = text[:i+1]
	}
	var r pickResponse
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return nil, i18n.E("ai.badJSON", err)
	}
	valid := make(map[string]bool, len(req.Candidates))
	byTitle := map[string]string{}
	for _, c := range req.Candidates {
		valid[c.ID] = true
		if t := strings.ToLower(strings.TrimSpace(c.Title)); t != "" {
			byTitle[t] = c.ID
		}
	}
	out := make([]Pick, 0, len(r.Picks))
	seen := map[string]bool{}
	for _, p := range r.Picks {
		id := candidateID(rawID(p.ID), p.Title, valid, byTitle)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, Pick{ID: id, Reason: p.Reason})
		}
	}
	if len(out) == 0 {
		// Show what came back: invented ids, titles, another format?
		return nil, i18n.E("ai.noPicks", excerpt(text, 300))
	}
	return out, nil
}

// candidateID maps what a model returned to a candidate id. Besides the exact
// id it accepts the id followed by more of the candidate line ("291300 | Film")
// and, as a last resort, the title instead of the id.
func candidateID(id, title string, valid map[string]bool, byTitle map[string]string) string {
	if valid[id] {
		return id
	}
	if i := strings.IndexAny(id, " |"); i > 0 && valid[id[:i]] {
		return id[:i]
	}
	for _, t := range []string{id, title} {
		if t = strings.ToLower(strings.TrimSpace(t)); t == "" {
			continue
		}
		if c, ok := byTitle[t]; ok {
			return c
		}
	}
	return ""
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + " …"
	}
	return s
}
