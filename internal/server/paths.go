package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// Direct file access: ffmpeg reads films and episodes from disk instead of
// through Plex. Outside a container Plex's paths are used as they are; in a
// container the media folders are mounted and each Plex library folder is
// assigned its path inside the container (Library page).

// inContainer reports whether Flimmerkiste runs in a container: our image sets
// FLIMMERKISTE_CONTAINER, Docker creates /.dockerenv in every container.
func inContainer() bool {
	if os.Getenv("FLIMMERKISTE_CONTAINER") == "1" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// pathMap returns the assigned folders: Plex library folder -> local folder.
func (s *Server) pathMap(ctx context.Context) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(s.store.Setting(ctx, store.SetPathMap, "{}")), &m)
	return m
}

// mapPath translates a path Plex reports into the local one, using the longest
// matching folder; folders only match whole (/data/movies is not /data/movies-4k).
func mapPath(m map[string]string, p string) (string, bool) {
	best, local := "", ""
	for from, to := range m {
		f := strings.TrimRight(from, "/")
		if f != "" && (p == f || strings.HasPrefix(p, f+"/")) && len(f) > len(best) {
			best, local = f, to
		}
	}
	if best == "" {
		return "", false
	}
	return filepath.Join(local, strings.TrimPrefix(p, best)), true
}

// localFile returns where ffmpeg can read the file Plex knows as plexPath, or
// "" to stream it from Plex as before.
func (s *Server) localFile(ctx context.Context, plexPath string) string {
	if plexPath == "" {
		return ""
	}
	p := plexPath
	if s.container {
		var ok bool
		if p, ok = mapPath(s.pathMap(ctx), plexPath); !ok {
			return ""
		}
	}
	if !readable(p) {
		return ""
	}
	return p
}

// readable reports whether p is a regular file this process can open.
func readable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	return err == nil && st.Mode().IsRegular()
}

// libraryLocation is a Plex library folder and where it is mounted here.
type libraryLocation struct {
	Plex  string `json:"plex"`
	Local string `json:"local"`
}

// libraryLocations returns the folders of each library as Plex reports them,
// with their assignments (nil when Plex is unreachable).
func (s *Server) libraryLocations(ctx context.Context) map[string][]libraryLocation {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	srv, err := s.mediaServer(ctx)
	if err != nil {
		return nil
	}
	sections, err := srv.Libraries(ctx)
	if err != nil {
		return nil
	}
	m := s.pathMap(ctx)
	out := map[string][]libraryLocation{}
	for _, sec := range sections {
		for _, p := range sec.Paths {
			out[sec.ID] = append(out[sec.ID], libraryLocation{Plex: p, Local: m[p]})
		}
	}
	return out
}

// pathCheckSamples: titles of a library checked after assigning its folders.
const pathCheckSamples = 5

// handlePutLibraryPaths assigns local folders to the Plex folders of a library
// and checks a few of its files under the new paths.
func (s *Server) handlePutLibraryPaths(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Paths map[string]string `json:"paths"` // Plex folder -> local folder ("" = none)
	}
	if err := readJSON(w, r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	known := map[string]bool{}
	for _, l := range s.libraryLocations(ctx)[r.PathValue("id")] {
		known[l.Plex] = true
	}
	m := s.pathMap(ctx)
	for from, to := range in.Paths {
		if !known[from] {
			s.fail(w, r, http.StatusBadRequest, "api.notFolder", from)
			return
		}
		to = strings.TrimSpace(to)
		if to == "" {
			delete(m, from)
			continue
		}
		to = filepath.Clean(to)
		if !filepath.IsAbs(to) {
			s.fail(w, r, http.StatusBadRequest, "api.absolutePath", to)
			return
		}
		if st, err := os.Stat(to); err != nil || !st.IsDir() {
			s.fail(w, r, http.StatusBadRequest, "api.noFolder", to)
			return
		}
		m[from] = to
	}
	raw, _ := json.Marshal(m)
	if err := s.store.SetSetting(ctx, store.SetPathMap, string(raw)); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.checkLibraryPaths(ctx, r.PathValue("id")))
}

// pathCheck is the result of reading sample files of a library directly.
type pathCheck struct {
	Checked int    `json:"checked"`
	Found   int    `json:"found"`
	Missing string `json:"missing,omitempty"` // a file that could not be read
}

// checkLibraryPaths looks up a few titles of a library, spread over it, and
// tries to open their files under the assigned folders.
func (s *Server) checkLibraryPaths(ctx context.Context, libraryID string) pathCheck {
	var res pathCheck
	items, err := s.lib.All(ctx)
	if err != nil {
		return res
	}
	var keys []string
	for _, m := range items {
		if m.LibraryID == libraryID {
			keys = append(keys, m.RatingKey)
		}
	}
	srv, err := s.mediaServer(ctx)
	if err != nil || len(keys) == 0 {
		return res
	}
	n := min(pathCheckSamples, len(keys))
	for i := 0; i < n; i++ {
		md, err := srv.Item(ctx, keys[i*len(keys)/n])
		if err != nil || len(md.Media) == 0 || len(md.Media[0].Parts) == 0 || md.Media[0].Parts[0].File == "" {
			continue
		}
		res.Checked++
		file := md.Media[0].Parts[0].File
		if s.localFile(ctx, file) != "" {
			res.Found++
		} else if res.Missing == "" {
			res.Missing = file
			if p, ok := mapPath(s.pathMap(ctx), file); ok && s.container {
				res.Missing = p
			}
		}
	}
	return res
}
