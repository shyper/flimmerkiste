#!/usr/bin/env python3
"""Tests of the helper scripts (needs ffmpeg and ffprobe):

    python3 -m unittest discover -s scripts -p 'test_*.py'

plex_subtitles.py and plex_themes.py run against a small fake Plex, trailers.py
against a fake TMDb and KinoCheck; the
Jellyfin and Emby scripts were tried against the test servers in test/mediaserver.
"""

import fcntl
import http.server
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))


def load(name):
    spec = importlib.util.spec_from_file_location(name, os.path.join(HERE, name + ".py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def ffmpeg(*args):
    subprocess.run(["ffmpeg", "-v", "error", "-y", *args], check=True)


def movie_with_subtitles(path, tmp):
    """A short video with a forced German and a full English SRT track inside."""
    for name, text in (("forced.srt", "Erzwungen"), ("full.srt", "Full")):
        with open(os.path.join(tmp, name), "w") as f:
            f.write(f"1\n00:00:01,000 --> 00:00:02,000\n{text}\n")
    ffmpeg("-f", "lavfi", "-i", "color=black:s=160x90:d=3", "-i", os.path.join(tmp, "forced.srt"), "-i", os.path.join(tmp, "full.srt"),
           "-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-c:s", "copy",
           "-metadata:s:s:0", "language=ger", "-disposition:s:0", "forced", "-metadata:s:s:1", "language=eng", path)


class FakePlex(http.server.BaseHTTPRequestHandler):
    calls = []

    def log_message(self, *a):
        pass

    def answer(self, body):
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        FakePlex.calls.append(("GET", self.path))
        if self.headers.get("X-Plex-Token") != "t":
            self.send_response(401)
            self.end_headers()
            return
        part = {"file": "/data/movies/Film/Film.mkv"}
        if self.path == "/library/sections":
            self.answer({"MediaContainer": {"Directory": [{"key": "1", "type": "movie", "title": "Filme"}]}})
        elif self.path.startswith("/library/sections/1/all"):
            self.answer({"MediaContainer": {"Metadata": [{"ratingKey": "7", "title": "Film", "Media": [{"Part": [part]}]}]}})
        elif self.path == "/library/metadata/7":
            streams = [{"streamType": 3, "codec": "srt", "forced": True, "index": 1, "languageCode": "ger"},
                       {"streamType": 3, "codec": "srt", "index": 2, "languageCode": "eng"}]
            self.answer({"MediaContainer": {"Metadata": [{"ratingKey": "7", "Media": [{"Part": [dict(part, Stream=streams)]}]}]}})
        else:
            self.send_response(404)
            self.end_headers()

    def do_PUT(self):
        FakePlex.calls.append(("PUT", self.path))
        self.send_response(200)
        self.end_headers()


class PlexSubtitlesTest(unittest.TestCase):
    def test_run(self):
        srv = http.server.HTTPServer(("127.0.0.1", 0), FakePlex)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        with tempfile.TemporaryDirectory() as tmp:
            folder = os.path.join(tmp, "movies", "Film")
            os.makedirs(folder)
            movie_with_subtitles(os.path.join(folder, "Film.mkv"), tmp)
            state = os.path.join(tmp, "state.json")
            cmd = [sys.executable, os.path.join(HERE, "plex_subtitles.py"), "--url", f"http://127.0.0.1:{srv.server_port}",
                   "--token", "t", "--map", f"/data={tmp}", "--state", state]

            out = subprocess.run(cmd + ["--dry-run"], capture_output=True, text=True, check=True).stdout
            self.assertIn("found: Film: Film.de.forced.srt", out)
            self.assertEqual(sorted(os.listdir(folder)), ["Film.mkv"])

            FakePlex.calls = []
            out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
            self.assertIn("saved: Film: Film.de.forced.srt", out)
            with open(os.path.join(folder, "Film.de.forced.srt")) as f:
                self.assertIn("Erzwungen", f.read())
            self.assertIn(("PUT", "/library/metadata/7/refresh"), FakePlex.calls)

            # unchanged file: skipped without asking Plex for the title
            FakePlex.calls = []
            out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
            self.assertIn("skipped: 1", out)
            self.assertNotIn(("GET", "/library/metadata/7"), FakePlex.calls)

            # --all: the English track too (a changed setting reads the file again)
            subprocess.run(cmd + ["--all", "--silent"], capture_output=True, text=True, check=True)
            self.assertEqual(sorted(os.listdir(folder)), ["Film.de.forced.srt", "Film.en.srt", "Film.mkv"])

            # a second run while one is going exits right away
            with open(state + ".lock", "w") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                r = subprocess.run(cmd, capture_output=True, text=True)
            self.assertEqual((r.returncode, r.stdout.strip()), (0, "plex_subtitles: already running"))

            # Plex unreachable: one line on stderr and exit code 1, also with --silent
            r = subprocess.run(cmd[:2] + ["--url", "http://127.0.0.1:1", "--token", "t", "--state", state, "--silent"],
                               capture_output=True, text=True)
            self.assertEqual(r.returncode, 1)
            self.assertTrue(r.stderr.startswith("plex_subtitles: "))
            self.assertEqual(r.stdout, "")


class FakeThemesPlex(http.server.BaseHTTPRequestHandler):
    """Show 1 has a theme hosted by Plex, show 2 a theme.mp3 next to it, show 3 none."""
    themes = {}
    calls = []

    def log_message(self, *a):
        pass

    def answer(self, body):
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        FakeThemesPlex.calls.append(("GET", self.path))
        if self.path == "/library/sections":
            self.answer({"MediaContainer": {"Directory": [{"key": "2", "type": "show", "title": "Serien"}]}})
        elif self.path.startswith("/library/sections/2/all"):
            items = [{"ratingKey": k, "title": f"Serie {k}", "year": 2000, "Guid": [{"id": f"tmdb://{k}00"}],
                      **({"theme": f"/library/metadata/{k}/theme/1"} if FakeThemesPlex.themes[k] else {})} for k in ("1", "2", "3")]
            self.answer({"MediaContainer": {"Metadata": items}})
        elif self.path.endswith("/themes"):
            self.answer({"MediaContainer": {"Metadata": FakeThemesPlex.themes[self.path.split("/")[3]]}})
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        FakeThemesPlex.calls.append(("POST", self.path))
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        k = self.path.split("/")[3]
        # like Plex: the upload is added, the theme in use stays selected
        FakeThemesPlex.themes[k].append({"ratingKey": f"upload://themes/{k}", "provider": "upload",
                                         "selected": not FakeThemesPlex.themes[k]})
        self.answer({})

    def do_PUT(self):
        FakeThemesPlex.calls.append(("PUT", self.path))
        k = self.path.split("/")[3]
        url = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)["url"][0]
        for t in FakeThemesPlex.themes[k]:
            t["selected"] = t["ratingKey"] == url
        self.answer({})


class PlexThemesTest(unittest.TestCase):
    def test_replace_plex(self):
        FakeThemesPlex.themes = {
            # as a Plex server lists them (no provider field)
            "1": [{"ratingKey": "metadata://themes/tv.plex.agents.series_863985220f37bbff6b3d0e6c0704dc7ef74281d9", "selected": True}],
            "2": [{"ratingKey": "local://theme.mp3", "provider": "local", "selected": True}],
            "3": [],
        }
        srv = http.server.HTTPServer(("127.0.0.1", 0), FakeThemesPlex)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        m = load("plex_themes")
        m.themerr_url = lambda kind, tmdb: f"https://youtube.example/{tmdb}"

        def download(ytdlp, url, tmp):
            path = os.path.join(tmp, "theme.mp3")
            with open(path, "wb") as f:
                f.write(b"ID3")
            return path
        m.download = download
        with tempfile.TemporaryDirectory() as tmp:
            def run(*flags):
                args = m.argparse.Namespace(url=f"http://127.0.0.1:{srv.server_port}", token="t", shows=True, dry_run=False,
                                            limit=0, ytdlp="yt-dlp", silent=True, retry_days=7,
                                            state=os.path.join(tmp, "state.json"), replace_plex=False)
                for f in flags:
                    setattr(args, f, True)
                FakeThemesPlex.calls = []
                m.run(args)

            # without the option only the show without a theme gets one
            run()
            self.assertEqual([c for c in FakeThemesPlex.calls if c[0] == "POST"], [("POST", "/library/metadata/3/themes")])
            self.assertTrue(FakeThemesPlex.themes["1"][0]["selected"])

            # with it the Plex theme is replaced and the new one selected; the theme.mp3 stays
            run("replace_plex")
            self.assertEqual([c for c in FakeThemesPlex.calls if c[0] != "GET"],
                             [("POST", "/library/metadata/1/themes"),
                              ("PUT", "/library/metadata/1/theme?url=" + urllib.parse.quote("upload://themes/1", safe=""))])
            self.assertEqual([t["ratingKey"] for t in FakeThemesPlex.themes["1"] if t["selected"]], ["upload://themes/1"])
            self.assertEqual([t["provider"] for t in FakeThemesPlex.themes["2"] if t["selected"]], ["local"])

            # done: a further run uploads nothing
            run("replace_plex")
            self.assertEqual([c for c in FakeThemesPlex.calls if c[0] != "GET"], [])


class FakeMovieAPIs(http.server.BaseHTTPRequestHandler):
    """TMDb and KinoCheck: movie 1 has a German trailer at KinoCheck, movie 2 only
    an English one at TMDb, movie 3 none."""
    calls = []

    def log_message(self, *a):
        pass

    def answer(self, body, code=200):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        q = dict(urllib.parse.parse_qsl(url.query))
        FakeMovieAPIs.calls.append(url.path)
        if self.headers.get("User-Agent", "").startswith("Python-urllib"):
            return self.answer({"title": "Error 1010: Access denied"}, 403)  # like KinoCheck's Cloudflare
        if url.path.startswith("/tmdb/") and q.get("api_key") != "k":
            return self.answer({}, 401)
        if url.path == "/tmdb/movie/now_playing":
            return self.answer({"total_pages": 1, "results": [{"id": 1, "title": "Neu"}, {"id": 2, "title": "Englisch"}]})
        if url.path == "/tmdb/movie/upcoming":
            return self.answer({"total_pages": 1, "results": [{"id": 2, "title": "Englisch"}, {"id": 3, "title": "Ohne"}]})
        if url.path.startswith("/tmdb/movie/"):
            mid = int(url.path.rsplit("/", 1)[1])
            videos = [{"site": "YouTube", "type": "Trailer", "iso_639_1": "en", "key": "en-old", "official": True, "published_at": "2026-01-01"},
                      {"site": "YouTube", "type": "Trailer", "iso_639_1": "en", "key": "en-new", "official": True, "published_at": "2026-05-01"},
                      {"site": "YouTube", "type": "Teaser", "iso_639_1": "en", "key": "teaser"}] if mid == 2 else []
            return self.answer({"id": mid, "title": {1: "Neu", 2: "Englisch", 3: "Ohne"}[mid], "release_date": "2026-11-01",
                                "overview": "Inhalt", "runtime": 110, "genres": [{"name": "Drama"}], "poster_path": "/p.jpg",
                                "backdrop_path": "/b.jpg", "external_ids": {"imdb_id": f"tt000000{mid}"}, "videos": {"results": videos},
                                "release_dates": {"results": [{"iso_3166_1": "DE", "release_dates": [
                                    {"type": 3, "release_date": "2026-11-12T00:00:00.000Z", "certification": "12"}]}]}})
        if url.path == "/kinocheck/movies":
            if q.get("tmdb_id") != "1" or q.get("language") != "de":
                return self.answer({}, 404)
            return self.answer({"tmdb_id": 1, "trailer": {"youtube_video_id": "de-1", "language": "de", "categories": ["Trailer"],
                                                          "title": "NEU Trailer German Deutsch (2026)"}, "videos": []})
        self.answer({}, 404)


class TrailersTest(unittest.TestCase):
    def test_run(self):
        srv = http.server.HTTPServer(("127.0.0.1", 0), FakeMovieAPIs)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        m = load("trailers")
        m.TMDB = f"http://127.0.0.1:{srv.server_port}/tmdb"
        m.KINOCHECK = f"http://127.0.0.1:{srv.server_port}/kinocheck"
        loaded = []

        def download(ytdlp, youtube_id, max_height, tmp):
            loaded.append((youtube_id, max_height))
            path = os.path.join(tmp, "trailer.mp4")
            ffmpeg("-f", "lavfi", "-i", "color=black:s=320x180:d=2", "-c:v", "libx264", path)
            return path
        m.download = download
        with tempfile.TemporaryDirectory() as folder:
            # a trailer of a movie released long ago, and its description
            with open(os.path.join(folder, "Alt (2025) [tmdbid-9] de.mp4"), "w"):
                pass
            with open(os.path.join(folder, "Alt (2025) [tmdbid-9] de.json"), "w") as f:
                json.dump({"video": "Alt (2025) [tmdbid-9] de.mp4", "tmdb": 9, "title": "Alt", "released": "2026-01-01"}, f)

            def run(today="2026-10-20"):
                args = m.argparse.Namespace(dir=folder, tmdb_key="k", languages=["de", "en"], region="DE", max_height=720,
                                            keep_days=60, pages=1, limit=0, retry_days=3, dry_run=False, silent=True,
                                            ytdlp="yt-dlp", ffprobe="ffprobe", state=os.path.join(folder, ".trailers_state.json"))
                m.run(args, today=m.datetime.date.fromisoformat(today))

            run()
            names = sorted(n for n in os.listdir(folder) if not n.startswith("."))
            self.assertEqual(names, ["Englisch (2026) [tmdbid-2] [imdbid-tt0000002] en.json", "Englisch (2026) [tmdbid-2] [imdbid-tt0000002] en.mp4",
                                     "Neu (2026) [tmdbid-1] [imdbid-tt0000001] de.json", "Neu (2026) [tmdbid-1] [imdbid-tt0000001] de.mp4"])
            # German from KinoCheck, the newest official English one from TMDb, in the height asked for
            self.assertEqual(sorted(loaded), [("de-1", 720), ("en-new", 720)])
            with open(os.path.join(folder, "Neu (2026) [tmdbid-1] [imdbid-tt0000001] de.json")) as f:
                d = json.load(f)
            self.assertEqual((d["video"], d["tmdb"], d["imdb"], d["released"], d["content_rating"], d["language"], d["genres"], d["height"]),
                             ("Neu (2026) [tmdbid-1] [imdbid-tt0000001] de.mp4", 1, "tt0000001", "2026-11-12", "de/12", "de", ["Drama"], 180))
            self.assertTrue(d["poster"].endswith("/w500/p.jpg") and 1900 < d["duration_ms"] < 2200, d)
            with open(os.path.join(folder, ".trailers_state.json")) as f:
                self.assertEqual(list(json.load(f)), ["3"])  # no trailer: looked for again after --retry-days

            # second run: nothing new, movie 3 waits
            loaded.clear()
            FakeMovieAPIs.calls = []
            run()
            self.assertEqual(loaded, [])
            self.assertNotIn("/tmdb/movie/3", FakeMovieAPIs.calls)

            # 60 days after the release the trailers go
            run(today="2027-01-20")
            self.assertEqual(sorted(n for n in os.listdir(folder) if not n.startswith(".")), [])


class HelpersTest(unittest.TestCase):
    def test_map_path(self):
        for name in ("plex_subtitles", "jellyfin_subtitles", "emby_subtitles", "jellyfin_themes", "emby_themes"):
            m = load(name)
            maps = [("/media", "/mnt/a"), ("/media/movies", "/mnt/b")]
            self.assertEqual(m.map_path("/media/movies/X/X.mkv", maps), "/mnt/b/X/X.mkv", name)  # longest prefix wins
            self.assertEqual(m.map_path("/media/shows/Y", maps), "/mnt/a/shows/Y", name)
            self.assertEqual(m.map_path("/mediathek/Z", maps), "/mediathek/Z", name)  # whole folder names only

    def test_targets(self):
        m = load("jellyfin_subtitles")
        with tempfile.TemporaryDirectory() as tmp:
            video = os.path.join(tmp, "Film.mkv")
            tracks = [{"index": 2, "codec": "subrip", "language": "de", "forced": True},
                      {"index": 3, "codec": "ass", "language": "en", "forced": False},
                      {"index": 4, "codec": "subrip", "language": "de", "forced": True}]  # same name: first wins
            names = [os.path.basename(n) for _, n in m.targets(video, tracks, False)]
            self.assertEqual(names, ["Film.de.forced.srt"])
            names = [os.path.basename(n) for _, n in m.targets(video, tracks, True)]
            self.assertEqual(names, ["Film.de.forced.srt", "Film.en.ass"])
            open(os.path.join(tmp, "Film.de.forced.srt"), "w").close()
            self.assertEqual(m.targets(video, tracks, False), [])  # existing files are left alone

    def test_forced_by_title(self):
        m = load("emby_subtitles")
        self.assertTrue(m.is_forced(False, "Deutsch (Erzwungen)"))
        self.assertTrue(m.is_forced(True, None))
        self.assertFalse(m.is_forced(False, "Deutsch"))

    def test_shared_folder(self):
        m = load("jellyfin_themes")
        with tempfile.TemporaryDirectory() as tmp:
            own, shared = os.path.join(tmp, "A"), os.path.join(tmp, "B")
            os.makedirs(own)
            os.makedirs(shared)
            for f in ("A.mkv", "A-trailer.mp4", "theme.mp3"):
                open(os.path.join(own, f), "w").close()
            for f in ("B1.mkv", "B2.avi"):
                open(os.path.join(shared, f), "w").close()
            self.assertEqual(m.title_folder({"Type": "Movie", "Path": os.path.join(own, "A.mkv")}, []), own)
            self.assertTrue(m.has_theme(own))
            self.assertIsNone(m.title_folder({"Type": "Movie", "Path": os.path.join(shared, "B1.mkv")}, []))
            self.assertEqual(m.title_folder({"Type": "Series", "Path": shared}, []), shared)


if __name__ == "__main__":
    unittest.main()
