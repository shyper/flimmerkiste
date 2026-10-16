// Package logging holds the log handler the application writes through.
package logging

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// Redacting wraps a handler so Plex tokens never reach the log, wherever a
// URL with one ends up (messages, strings, errors, groups). Code should still
// redact at the source; this is the safety net.
func Redacting(h slog.Handler) slog.Handler { return redactHandler{h} }

type redactHandler struct{ slog.Handler }

func (h redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, mediaserver.Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.Handler.Handle(ctx, out)
}

func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redactAttr(a)
	}
	return redactHandler{h.Handler.WithAttrs(clean)}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{h.Handler.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, mediaserver.Redact(v.String()))
	case slog.KindGroup:
		group := v.Group()
		clean := make([]slog.Attr, len(group))
		for i, g := range group {
			clean[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(clean...)}
	case slog.KindAny:
		// errors, URLs, structs: compare their text form
		s := fmt.Sprint(v.Any())
		if r := mediaserver.Redact(s); r != s {
			return slog.String(a.Key, r)
		}
	}
	return a
}
