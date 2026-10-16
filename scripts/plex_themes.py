#!/usr/bin/env python3
"""Uploads theme songs from ThemerrDB for movies (and shows) to Plex.

For every title without a theme song, ThemerrDB is looked up by its TMDB id.
If there is an entry, yt-dlp downloads the audio from YouTube as MP3 and the
script uploads it as the theme song through the Plex API. No access to the
media files is needed; the script can run on any computer that reaches Plex.

Meant for regular runs (e.g. from cron): titles ThemerrDB doesn't know are only
looked up again after --retry-days (state in --state), as are videos yt-dlp
could not download (unless ThemerrDB names another one by then).
With --silent there is no output, only a line on stderr and exit code 1 when the
whole run fails (Plex not reachable).

Needs: Python 3.9+, a current yt-dlp (old versions get "403 Forbidden" from
YouTube), ffmpeg and deno or node for yt-dlp.

    PLEX_URL=https://plex.example.org PLEX_TOKEN=xxxx ./plex_themes.py --dry-run
    PLEX_URL=https://plex.example.org PLEX_TOKEN=xxxx ./plex_themes.py
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

THEMERRDB = "https://app.lizardbyte.dev/ThemerrDB"
DB_KIND = {"movie": "movies", "show": "tv_shows"}


def plex(base, token, path, method="GET", data=None):
    req = urllib.request.Request(base.rstrip("/") + path, data=data, method=method,
                                 headers={"X-Plex-Token": token, "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        body = r.read()
    return json.loads(body) if body else None


def tmdb_id(item):
    for g in item.get("Guid", []):
        if g["id"].startswith("tmdb://"):
            return g["id"][len("tmdb://"):]
    return None


def themerr_url(kind, tmdb):
    """YouTube link from ThemerrDB, None if the title is not in it."""
    try:
        with urllib.request.urlopen(f"{THEMERRDB}/{DB_KIND[kind]}/themoviedb/{tmdb}.json", timeout=30) as r:
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
    ap.add_argument("--url", default=os.environ.get("PLEX_URL"), help="Plex address (or PLEX_URL)")
    ap.add_argument("--token", default=os.environ.get("PLEX_TOKEN"), help="Plex token (or PLEX_TOKEN)")
    ap.add_argument("--shows", action="store_true", help="also handle shows without a theme song")
    ap.add_argument("--dry-run", action="store_true", help="only look up, download and upload nothing")
    ap.add_argument("--limit", type=int, default=0, help="at most this many downloads per run (0 = all)")
    ap.add_argument("--yt-dlp", dest="ytdlp", default="yt-dlp", help="path to yt-dlp")
    ap.add_argument("--silent", action="store_true",
                    help="no output, not even errors of single titles (for cron); only when Plex is not reachable")
    ap.add_argument("--retry-days", type=float, default=7, help="try titles without an entry or with a failed download again only after this many days")
    ap.add_argument("--state", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "plex_themes_state.json"),
                    help="state file")
    args = ap.parse_args()
    if not args.url or not args.token:
        ap.error("give the Plex address and token (--url/--token or PLEX_URL/PLEX_TOKEN)")

    try:
        run(args)
    except (urllib.error.URLError, OSError, ValueError) as e:
        # the whole run failed (Plex or ThemerrDB unreachable …): one line, also with --silent
        print(f"plex_themes: {e}", file=sys.stderr)
        sys.exit(1)


def run(args):
    def say(msg, err=False):
        if not args.silent:
            print(msg, file=sys.stderr if err else sys.stdout)

    kinds = ("movie", "show") if args.shows else ("movie",)
    state = {} if args.dry_run else load_state(args.state)
    now = time.time()
    stats = {"present": 0, "no TMDB id": 0, "not in ThemerrDB": 0, "skipped": 0,
             "found": 0, "uploaded": 0, "failed": 0}

    sections = plex(args.url, args.token, "/library/sections")["MediaContainer"].get("Directory", [])
    for sec in sections:
        if sec["type"] not in kinds:
            continue
        items = plex(args.url, args.token, f"/library/sections/{sec['key']}/all?includeGuids=1")["MediaContainer"].get("Metadata", [])
        say(f"== {sec['title']}: {len(items)} titles")
        for it in items:
            name = f"{it['title']} ({it.get('year', '?')})"
            if it.get("theme"):
                stats["present"] += 1
                continue
            tmdb = tmdb_id(it)
            if not tmdb:
                stats["no TMDB id"] += 1
                continue
            seen = state.get(it["ratingKey"])
            recent = seen and seen.get("tmdb") == tmdb and now - seen["checked"] < args.retry_days * 86400
            if recent and "failed" not in seen:
                stats["skipped"] += 1
                continue
            url = themerr_url(sec["type"], tmdb)
            if not url:
                stats["not in ThemerrDB"] += 1
                state[it["ratingKey"]] = {"tmdb": tmdb, "checked": now}
                continue
            # a video that failed recently waits too, unless ThemerrDB has a new one
            if recent and seen["failed"] == url:
                stats["skipped"] += 1
                continue
            stats["found"] += 1
            if args.dry_run:
                say(f"   found: {name}")
                continue
            if args.limit and stats["uploaded"] + stats["failed"] >= args.limit:
                continue
            try:
                with tempfile.TemporaryDirectory() as tmp:
                    with open(download(args.ytdlp, url, tmp), "rb") as f:
                        plex(args.url, args.token, f"/library/metadata/{it['ratingKey']}/themes", method="POST", data=f.read())
                stats["uploaded"] += 1
                state.pop(it["ratingKey"], None)
                say(f"   uploaded: {name}")
            except DownloadError as e:
                # mostly for good (video removed, blocked): tried again after --retry-days
                stats["failed"] += 1
                state[it["ratingKey"]] = {"tmdb": tmdb, "checked": now, "failed": url}
                say(f"   ERROR for {name}: {e}", err=True)
            except (subprocess.SubprocessError, OSError, urllib.error.URLError) as e:
                # timeouts, Plex unreachable …: the next run tries again
                stats["failed"] += 1
                say(f"   ERROR for {name}: {e}", err=True)
            save_state(args.state, state)

    if not args.dry_run:
        save_state(args.state, state)
    say("== " + ", ".join(f"{k}: {v}" for k, v in stats.items()))


if __name__ == "__main__":
    main()
