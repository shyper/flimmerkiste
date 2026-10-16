package plex

import "github.com/shyper/flimmerkiste/internal/mediaserver"

// Redact hides Plex tokens in URLs inside s (error messages, ffmpeg output,
// command lines), so they never reach a log.
func Redact(s string) string { return mediaserver.Redact(s) }
