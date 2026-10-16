package stream

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultMusicPath(t *testing.T) {
	tmp := t.TempDir()
	s := &Streamer{CacheDir: tmp}

	path := s.DefaultMusicPath()
	if path == "" {
		t.Fatal("expected music path, got empty")
	}
	if filepath.Base(path) != "intermission.mp3" {
		t.Fatalf("expected intermission.mp3, got %s", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected non-empty file")
	}

	// Calling again should return cached path without error
	path2 := s.DefaultMusicPath()
	if path2 != path {
		t.Fatalf("expected identical path, got %s", path2)
	}
}
