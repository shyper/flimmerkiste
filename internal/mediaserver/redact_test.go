package mediaserver

import "testing"

func TestRedactHeadersAndKeys(t *testing.T) {
	for in, want := range map[string]string{
		"X-Plex-Token: abc123 sent":                         "X-Plex-Token: *** sent",
		"Authorization: Bearer eyJhbGciOi.xyz":              "Authorization: Bearer ***",
		"Incorrect API key provided: sk-proj-abcdef1234567": "Incorrect API key provided: sk-***",
		"sk-ant-api03-AbCdEf123456":                         "sk-***",
		"skip this":                                         "skip this",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
