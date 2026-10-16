package server

import "testing"

func TestMapPath(t *testing.T) {
	m := map[string]string{"/data/movies": "/movies", "/data/movies-4k/": "/uhd", "/data/movies/kids": "/kids"}
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"/data/movies/Matrix (1999)/Matrix.mkv", "/movies/Matrix (1999)/Matrix.mkv", true},
		{"/data/movies-4k/Dune.mkv", "/uhd/Dune.mkv", true},    // not /data/movies
		{"/data/movies/kids/Cars.mkv", "/kids/Cars.mkv", true}, // the longest folder wins
		{"/data/moviesX/a.mkv", "", false},
		{"/data/tv/a.mkv", "", false},
	} {
		got, ok := mapPath(m, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
