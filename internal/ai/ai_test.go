package ai

import (
	"strings"
	"testing"
)

func TestParsePicks(t *testing.T) {
	req := Request{Candidates: []Candidate{{ID: "291300"}, {ID: "show:5"}, {ID: "12345678901234567"}}}
	// Numbers instead of strings, a code fence, an unknown id and a duplicate.
	text := "```json\n{\"picks\":[{\"id\":291300,\"reason\":\"a\"},{\"id\":\"show:5\",\"reason\":\"b\"}," +
		"{\"id\":12345678901234567,\"reason\":\"c\"},{\"id\":42,\"reason\":\"x\"},{\"id\":\"291300\",\"reason\":\"d\"}]}\n```"
	picks, err := parsePicks(text, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(picks) != 3 || picks[0].ID != "291300" || picks[1].ID != "show:5" || picks[2].ID != "12345678901234567" || picks[0].Reason != "a" {
		t.Fatalf("unexpected picks: %+v", picks)
	}
	if _, err := parsePicks(`{"picks":[{"id":7,"reason":"x"}]}`, req); err == nil {
		t.Fatal("only unknown ids must be an error")
	}
}

func TestParsePicksFallbacks(t *testing.T) {
	req := Request{Candidates: []Candidate{{ID: "11", Title: "Der Frosch mit der Maske"}, {ID: "12", Title: "Die Bande des Schreckens"}, {ID: "13", Title: "Der Hexer"}}}
	// The id followed by more of the candidate line, a title instead of the id,
	// a title next to an invented id.
	text := `{"picks":[{"id":"11 | Film | Der Frosch mit der Maske","reason":"a"},{"id":"Die Bande des Schreckens","reason":"b"},{"id":"999","title":"der hexer","reason":"c"}]}`
	picks, err := parsePicks(text, req)
	if err != nil || len(picks) != 3 || picks[0].ID != "11" || picks[1].ID != "12" || picks[2].ID != "13" {
		t.Fatalf("unexpected: %+v %v", picks, err)
	}
	// Nothing usable: the error shows what came back.
	_, err = parsePicks(`{"picks":[{"id":"tt0054371","reason":"x"}]}`, req)
	if err == nil || !strings.Contains(err.Error(), "tt0054371") {
		t.Fatalf("error should quote the answer: %v", err)
	}
}
