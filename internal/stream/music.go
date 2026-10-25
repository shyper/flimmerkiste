package stream

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed assets/intermission.mp3
var defaultCardMusic []byte

// DefaultMusicPath extracts the embedded intermission music to the cache directory
// and returns its path for FFmpeg.
func (s *Streamer) DefaultMusicPath() string {
	if s.CacheDir == "" || len(defaultCardMusic) == 0 {
		return ""
	}
	target := filepath.Join(s.CacheDir, "intermission.mp3")
	if info, err := os.Stat(target); err == nil && info.Size() == int64(len(defaultCardMusic)) {
		return target
	}
	_ = os.MkdirAll(s.CacheDir, 0o755)
	// written aside and renamed, so a parallel stream never plays half a file
	f, err := os.CreateTemp(s.CacheDir, "intermission-*.tmp")
	if err != nil {
		return ""
	}
	_, err = f.Write(defaultCardMusic)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), target)
	}
	if err != nil {
		os.Remove(f.Name())
		return ""
	}
	return target
}
