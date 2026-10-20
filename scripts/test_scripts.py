#!/usr/bin/env python3
"""Tests of the helper scripts (needs ffmpeg and ffprobe):

    python3 -m unittest discover -s scripts -p 'test_*.py'

plex_subtitles.py and plex_themes.py run against a small fake Plex; the
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
