package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestRedacting(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(Redacting(slog.NewTextHandler(&buf, nil))).With("base", "http://h?X-Plex-Token=a1")
	u, _ := url.Parse("http://h/p?X-Plex-Token=b2")
	log.WithGroup("g").Info("open http://h?X-Plex-Token=c3 failed",
		"err", errors.New(`Get "http://h/f?download=0&X-Plex-Token=d4": EOF`), "url", u,
		slog.Group("inner", "s", "x-plex-token=e5"), "n", 3)
	out := buf.String()
	for _, secret := range []string{"a1", "b2", "c3", "d4", "e5"} {
		if strings.Contains(out, secret) {
			t.Fatalf("token %s in log: %s", secret, out)
		}
	}
	if !strings.Contains(out, "X-Plex-Token=***") || !strings.Contains(out, "g.n=3") {
		t.Fatalf("unexpected output: %s", out)
	}
}
