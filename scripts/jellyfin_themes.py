#!/usr/bin/env python3
"""Puts theme songs from ThemerrDB next to movies (and shows) for Jellyfin.

Jellyfin cannot take theme songs through its API; it plays a file "theme.mp3"
in the folder of a movie or show. For every title without one, ThemerrDB is
looked up by its TMDB id. If there is an entry, yt-dlp downloads the audio
from YouTube as MP3, the script saves it as theme.mp3 in the title's folder
and asks Jellyfin to refresh the title. Movies sharing a folder with other
movies are left out (the song would belong to all of them).

The script needs write access to the media folders: run it where the files
are, and if Jellyfin sees them under other paths, translate them with --map
(e.g. --map /media/movies=/mnt/media/movies).

Meant for regular runs (e.g. from cron): titles ThemerrDB doesn't know are only
looked up again after --retry-days (state in --state), as are videos yt-dlp
could not download (unless ThemerrDB names another one by then). A second run
while one is still going exits right away. With --silent there is no output,
only a line on stderr and exit code 1 when the whole run fails (Jellyfin not
reachable).

Needs: Python 3.9+, a current yt-dlp (old versions get "403 Forbidden" from
YouTube), ffmpeg and deno or node for yt-dlp, and an API key (Jellyfin
dashboard → API Keys).

    JELLYFIN_URL=http://jellyfin.local:8096 JELLYFIN_API_KEY=xxxx ./jellyfin_themes.py --dry-run
    JELLYFIN_URL=http://jellyfin.local:8096 JELLYFIN_API_KEY=xxxx ./jellyfin_themes.py --map /media=/mnt/media
"""

import argparse
import fcntl
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

THEMERRDB = "https://app.lizardbyte.dev/ThemerrDB"
DB_KIND = {"Movie": "movies", "Series": "tv_shows"}
VIDEO_EXT = {".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".ts", ".m2ts", ".mpg", ".mpeg", ".webm", ".iso"}
# extras next to a movie that do not make it a shared folder
EXTRA_SUFFIXES = ("-trailer", "-sample", "-featurette", "-behindthescenes", "-deleted", "-interview", "-scene", "-short", "-other")


def api(base, key, path, method="GET"):
    req = urllib.request.Request(base.rstrip("/") + "" + path, method=method, data=b"" if method == "POST" else None,
                                 headers={"Authorization": f'MediaBrowser Token="{key}"', "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        body = r.read()
    return json.loads(body) if body else None


def items(base, key, types):
    """All titles of the given types with their paths and ids, page by page."""
    start = 0
    while True:
        q = urllib.parse.urlencode({"Recursive": "true", "IncludeItemTypes": ",".join(types), "Fields": "Path,ProviderIds,ProductionYear",
                                    "StartIndex": start, "Limit": 500})
        page = api(base, key, "/Items?" + q)
        for it in page.get("Items", []):
            yield it
        start += len(page.get("Items", []))
        if not page.get("Items") or start >= page.get("TotalRecordCount", 0):
            return


def map_path(path, maps):
    """The path of a file as the server names it, translated for this computer."""
    for server, local in sorted(maps, key=lambda m: -len(m[0])):
        if path == server or path.startswith(server.rstrip("/") + "/"):
            return local.rstrip("/") + path[len(server.rstrip("/")):]
    return path


def title_folder(item, maps):
    """The folder the theme song belongs in, None if the movie shares its folder."""
    local = map_path(item["Path"], maps)
    if os.path.isdir(local):  # shows, and movies stored as a folder (DVD, Blu-ray)
        return local
    folder = os.path.dirname(local)
    if item["Type"] == "Movie":
        videos = [f for f in os.listdir(folder) if os.path.splitext(f)[1].lower() in VIDEO_EXT
                  and not os.path.splitext(f)[0].lower().endswith(EXTRA_SUFFIXES)]
        if len(videos) > 1:
            return None
    return folder


def has_theme(folder):
    return os.path.exists(os.path.join(folder, "theme.mp3")) or os.path.isdir(os.path.join(folder, "theme-music"))


def themerr_url(db, kind, tmdb):
    """YouTube link from ThemerrDB, None if the title is not in it."""
    try:
        with urllib.request.urlopen(f"{db}/{DB_KIND[kind]}/themoviedb/{tmdb}.json", timeout=30) as r:
            return json.load(r).get("youtube_theme_url")
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise


class DownloadError(Exception):
    pass


def download(ytdlp, url, tmp):
    out = os.path.join(tmp, "theme.%(ext)s")
    cmd = [ytdlp, "--quiet", "--no-warnings", "--no-update", "--no-playlist", "-x",
           "--audio-format", "mp3", "--audio-quality", "5", "-o", out]
    # yt-dlp only looks for deno by itself; without a JavaScript runtime
    # YouTube refuses many downloads
    if not shutil.which("deno") and shutil.which("node"):
        cmd += ["--js-runtimes", "node"]
    r = subprocess.run(cmd + [url], capture_output=True, text=True, timeout=600)
    if r.returncode != 0:
        # only yt-dlp's last line ("ERROR: … This video is not available")
        lines = r.stderr.strip().splitlines() or [f"yt-dlp: exit code {r.returncode}"]
        raise DownloadError(lines[-1])
    return os.path.join(tmp, "theme.mp3")


def save_theme(mp3, folder):
    """Copies the song into the folder under a temporary name first, so the
    server never reads half a file."""
    tmp = os.path.join(folder, ".theme.mp3.part")
    shutil.copyfile(mp3, tmp)
    os.replace(tmp, os.path.join(folder, "theme.mp3"))


def load_state(path):
    try:
        with open(path) as f:
            return json.load(f)
    except FileNotFoundError:
        return {}


def save_state(path, state):
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(state, f, indent=1)
    os.replace(tmp, path)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url", default=os.environ.get("JELLYFIN_URL"), help="Jellyfin address (or JELLYFIN_URL)")
    ap.add_argument("--api-key", dest="key", default=os.environ.get("JELLYFIN_API_KEY"), help="Jellyfin API key (or JELLYFIN_API_KEY)")
    ap.add_argument("--map", action="append", default=[], metavar="SERVER=LOCAL",
                    help="translate a folder as Jellyfin sees it to the folder on this computer (repeatable)")
    ap.add_argument("--shows", action="store_true", help="also handle shows without a theme song")
    ap.add_argument("--dry-run", action="store_true", help="only look up, download and write nothing")
    ap.add_argument("--limit", type=int, default=0, help="at most this many downloads per run (0 = all)")
    ap.add_argument("--yt-dlp", dest="ytdlp", default="yt-dlp", help="path to yt-dlp")
    ap.add_argument("--themerrdb", default=THEMERRDB, help=argparse.SUPPRESS)  # tests
    ap.add_argument("--silent", action="store_true",
                    help="no output, not even errors of single titles (for cron); only when Jellyfin is not reachable")
    ap.add_argument("--retry-days", type=float, default=7, help="try titles without an entry or with a failed download again only after this many days")
    ap.add_argument("--state", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "jellyfin_themes_state.json"),
                    help="state file")
    args = ap.parse_args()
    if not args.url or not args.key:
        ap.error("give the Jellyfin address and API key (--url/--api-key or JELLYFIN_URL/JELLYFIN_API_KEY)")
    try:
        args.maps = [tuple(m.split("=", 1)) for m in args.map]
        if any(len(m) != 2 or not m[0] or not m[1] for m in args.maps):
            raise ValueError
    except ValueError:
        ap.error("--map needs SERVER=LOCAL")

    # one run at a time (cron may start the next one while a long one is going)
    lock = open(args.state + ".lock", "w")
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        if not args.silent:
            print("jellyfin_themes: already running")
        return
    try:
        run(args)
    except (urllib.error.URLError, OSError, ValueError) as e:
        # the whole run failed (Jellyfin or ThemerrDB unreachable …): one line, also with --silent
        print(f"jellyfin_themes: {e}", file=sys.stderr)
        sys.exit(1)


def run(args):
    def say(msg, err=False):
        if not args.silent:
            print(msg, file=sys.stderr if err else sys.stdout)

    types = ["Movie", "Series"] if args.shows else ["Movie"]
    state = {} if args.dry_run else load_state(args.state)
    now = time.time()
    stats = {"present": 0, "no TMDB id": 0, "shared folder": 0, "missing folder": 0, "not in ThemerrDB": 0,
              "skipped": 0, "found": 0, "saved": 0, "failed": 0}
    saved = 0

    for it in items(args.url, args.key, types):
        if not it.get("Path"):
            continue
        name = f"{it.get('Name', '?')} ({it.get('ProductionYear', '?')})"
        try:
            folder = title_folder(it, args.maps)
        except OSError:
            stats["missing folder"] += 1
            say(f"   not readable here: {map_path(it['Path'], args.maps)} (see --map)", err=True)
            continue
        if folder is None:
            stats["shared folder"] += 1
            continue
        if not os.path.isdir(folder):
            stats["missing folder"] += 1
            say(f"   not readable here: {folder} (see --map)", err=True)
            continue
        if has_theme(folder):
            stats["present"] += 1
            continue
        tmdb = (it.get("ProviderIds") or {}).get("Tmdb")
        if not tmdb:
            stats["no TMDB id"] += 1
            continue
        seen = state.get(it["Id"])
        recent = seen and seen.get("tmdb") == tmdb and now - seen["checked"] < args.retry_days * 86400
        if recent and "failed" not in seen:
            stats["skipped"] += 1
            continue
        url = themerr_url(args.themerrdb, it["Type"], tmdb)
        if not url:
            stats["not in ThemerrDB"] += 1
            state[it["Id"]] = {"tmdb": tmdb, "checked": now}
            continue
        # a video that failed recently waits too, unless ThemerrDB has a new one
        if recent and seen["failed"] == url:
            stats["skipped"] += 1
            continue
        stats["found"] += 1
        if args.dry_run:
            say(f"   found: {name}")
            continue
        if args.limit and saved + stats["failed"] >= args.limit:
            continue
        try:
            with tempfile.TemporaryDirectory() as tmp:
                save_theme(download(args.ytdlp, url, tmp), folder)
            saved += 1
            stats["saved"] += 1
            state.pop(it["Id"], None)
            say(f"   saved: {name}")
            try:
                api(args.url, args.key, f"/Items/{it['Id']}/Refresh?MetadataRefreshMode=Default", method="POST")
            except urllib.error.URLError as e:
                say(f"   refresh of {name} failed ({e}); the next library scan finds the song", err=True)
        except DownloadError as e:
            # mostly for good (video removed, blocked): tried again after --retry-days
            stats["failed"] += 1
            state[it["Id"]] = {"tmdb": tmdb, "checked": now, "failed": url}
            say(f"   ERROR for {name}: {e}", err=True)
        except (subprocess.SubprocessError, OSError) as e:
            # timeouts, no write access …: the next run tries again
            stats["failed"] += 1
            say(f"   ERROR for {name}: {e}", err=True)
        save_state(args.state, state)

    if not args.dry_run:
        save_state(args.state, state)
    say("== " + ", ".join(f"{k}: {v}" for k, v in stats.items()))


if __name__ == "__main__":
    main()
