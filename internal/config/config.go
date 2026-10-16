package config

import (
	"os"
	"strconv"
)

// Config holds process-level settings that come from the environment.
// Everything the user can change at runtime lives in the database instead.
type Config struct {
	Listen   string // address the HTTP server binds to
	DataDir  string // SQLite database, uploaded logos, cache
	BaseURL  string // optional fixed external URL (otherwise derived from the request)
	PlexURL  string // optional override for the Plex server URL
	FFmpeg   string // ffmpeg binary
	VAAPI    string // GPU render node for hardware encoding (used if present)
	NVIDIA   string // NVIDIA device for NVENC, tried when VAAPI is not usable
	V4L2     string // V4L2 hardware encoder (Raspberry Pi 4), tried when VAAPI and NVENC are not usable
	LogLevel string
	Horizon  int // hours of schedule to keep generated ahead
	DevCORS  bool
	// TunerAllow extends who may use the tuner endpoints: "all", or IPs/CIDRs
	// in addition to the local network.
	TunerAllow string
}

func Load() Config {
	return Config{
		Listen:   env("FLIMMERKISTE_LISTEN", ":8089"),
		DataDir:  env("FLIMMERKISTE_DATA_DIR", "./data"),
		BaseURL:  env("FLIMMERKISTE_BASE_URL", ""),
		PlexURL:  env("FLIMMERKISTE_PLEX_URL", ""),
		FFmpeg:   env("FLIMMERKISTE_FFMPEG", "ffmpeg"),
		VAAPI:    env("FLIMMERKISTE_VAAPI_DEVICE", "/dev/dri/renderD128"),
		NVIDIA:   env("FLIMMERKISTE_NVIDIA_DEVICE", "/dev/nvidia0"),
		V4L2:     env("FLIMMERKISTE_V4L2_DEVICE", "/dev/video11"),
		LogLevel: env("FLIMMERKISTE_LOG_LEVEL", "info"),
		Horizon:  envInt("FLIMMERKISTE_EPG_HOURS", 48),
		DevCORS:  env("FLIMMERKISTE_DEV", "") == "1",

		TunerAllow: env("FLIMMERKISTE_TUNER_ALLOW", ""),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}
