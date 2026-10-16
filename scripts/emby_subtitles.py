#!/usr/bin/env python3
"""Saves text subtitles embedded in video files (e.g. SRT or ASS in an MKV) as
files of their own next to the video, for Emby.

Flimmerkiste burns subtitles in from files and needs them ahead; files of
their own work for every channel and every Emby app alike. This script finds titles with such tracks, copies them out with ffmpeg
once and names the file the way Emby reads it ("Movie (1999).de.forced.srt"),
then asks Emby to refresh the title. By default only forced tracks (for
foreign-language parts) are saved; --all saves every text track.

ffmpeg reads the whole video file once (subtitles are spread over it), with low
priority. The script needs write access to the media folders: run it where
the files are, and if Emby sees them under other paths, translate them with
--map (e.g. --map /media/movies=/mnt/media/movies).

Meant for regular runs (e.g. from cron): files already handled are skipped
until they change (state in --state), failed ones are tried again after
--retry-days. A second run while one is still going exits right away.
With --silent there is no output, only a line on stderr and exit code 1 when
the whole run fails (Emby not reachable).

Needs: Python 3.9+, ffmpeg and ffprobe, and an API key (Emby dashboard →
API Keys).

    EMBY_URL=http://emby.local:8096 EMBY_API_KEY=xxxx ./emby_subtitles.py --dry-run
    EMBY_URL=http://emby.local:8096 EMBY_API_KEY=xxxx ./emby_subtitles.py --map /media=/mnt/media
"""

import argparse
import fcntl
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# text subtitle codecs as ffprobe names them
PROBE_TEXT = {"subrip", "srt", "ass", "ssa", "mov_text", "webvtt", "text"}
# two-letter codes for file names, which every media server reads
LANG2 = {"ger": "de", "deu": "de", "eng": "en", "fre": "fr", "fra": "fr", "spa": "es", "ita": "it", "dut": "nl", "nld": "nl",
         "por": "pt", "pol": "pl", "rus": "ru", "jpn": "ja", "chi": "zh", "zho": "zh", "kor": "ko", "tur": "tr", "swe": "sv",
         "dan": "da", "nor": "no", "nob": "nb", "fin": "fi", "cze": "cs", "ces": "cs", "hun": "hu", "gre": "el", "ell": "el",
         "heb": "he", "ara": "ar", "hin": "hi", "tha": "th", "ukr": "uk", "rum": "ro", "ron": "ro", "bul": "bg", "hrv": "hr",
         "srp": "sr", "slv": "sl", "slo": "sk", "slk": "sk", "ice": "is", "isl": "is", "lat": "la", "kli": "tlh"}


def api(base, key, path, method="GET"):
    req = urllib.request.Request(base.rstrip("/") + "/emby" + path, method=method, data=b"" if method == "POST" else None,
                                 headers={"X-Emby-Token": key, "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        body = r.read()
    return json.loads(body) if body else None


def items(base, key, types):
    """All videos of the given types with their paths and streams, page by page."""
    start = 0
    while True:
        q = urllib.parse.urlencode({"Recursive": "true", "IncludeItemTypes": ",".join(types), "Fields": "Path,MediaStreams",
                                    "StartIndex": start, "Limit": 500})
        page = api(base, key, "/Items?" + q)
        for it in page.get("Items", []):
            yield it
        start += len(page.get("Items", []))
        if not page.get("Items") or start >= page.get("TotalRecordCount", 0):
            return


def is_forced(forced, title):
    """Forced by flag, or by a track title such as "Forced" or "Erzwungen"."""
    t = (title or "").lower()
    return bool(forced) or "forced" in t or "erzwungen" in t


def map_path(path, maps):
    """The path of a file as the server names it, translated for this computer."""
    for server, local in sorted(maps, key=lambda m: -len(m[0])):
        if path == server or path.startswith(server.rstrip("/") + "/"):
            return local.rstrip("/") + path[len(server.rstrip("/")):]
    return path


def probe_tracks(ffprobe, path):
    """Text subtitle tracks of a file: index, codec, language, forced."""
    r = subprocess.run([ffprobe, "-v", "error", "-select_streams", "s", "-show_entries",
                        "stream=index,codec_name:stream_tags=language,title:stream_disposition=forced",
                        "-of", "json", path], capture_output=True, text=True, timeout=300)
    if r.returncode != 0:
        raise ExtractError((r.stderr.strip().splitlines() or ["ffprobe failed"])[-1])
    out = []
    for s in json.loads(r.stdout or "{}").get("streams", []):
        if s.get("codec_name") not in PROBE_TEXT:
            continue
        tags = s.get("tags", {})
        lang = (tags.get("language") or "und").lower()
        out.append({"index": s["index"], "codec": s["codec_name"], "language": LANG2.get(lang, lang),
                    "forced": is_forced(s.get("disposition", {}).get("forced"), tags.get("title"))})
    return out


class ExtractError(Exception):
    pass


def targets(path, tracks, want_all):
    """Which tracks to save under which name; a name that exists is left alone."""
    base = os.path.splitext(path)[0]
    out, taken = [], set()
    for t in tracks:
        if not (t["forced"] or want_all):
            continue
        ext = ".ass" if t["codec"] in ("ass", "ssa") else ".srt"
        name = base + "." + t["language"] + (".forced" if t["forced"] else "") + ext
        if name in taken:
            continue  # a second track of the same kind: the first one wins
        taken.add(name)
        if not os.path.exists(name):
            out.append((t, name))
    return out


def extract(ffmpeg, path, todo):
    """Copies all tracks out in one pass over the file (temporary names first)."""
    cmd = [ffmpeg, "-v", "error", "-nostdin", "-y", "-i", path]
    tmps = []
    for t, name in todo:
        fmt = "ass" if name.endswith(".ass") else "srt"
        tmp = name + ".part"
        cmd += ["-map", f"0:{t['index']}", "-c:s", "copy" if fmt == "ass" else "srt", "-f", fmt, tmp]
        tmps.append((tmp, name))
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=4 * 3600, preexec_fn=lambda: os.nice(10))
        if r.returncode != 0:
            raise ExtractError((r.stderr.strip().splitlines() or [f"ffmpeg: exit code {r.returncode}"])[-1])
        for tmp, name in tmps:
            if os.path.getsize(tmp) == 0:
                raise ExtractError(f"empty subtitles: {os.path.basename(name)}")
        for tmp, name in tmps:
            os.replace(tmp, name)
    finally:
        for tmp, _ in tmps:
            if os.path.exists(tmp):
                os.remove(tmp)


def wanted_streams(item, want_all):
    """Whether the server lists an embedded text track worth a look."""
    for s in item.get("MediaStreams", []):
        if s.get("Type") != "Subtitle" or s.get("IsExternal") or not s.get("IsTextSubtitleStream"):
            continue
        if want_all or is_forced(s.get("IsForced"), s.get("Title") or s.get("DisplayTitle")):
            return True
    return False


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
    ap.add_argument("--url", default=os.environ.get("EMBY_URL"), help="Emby address (or EMBY_URL)")
    ap.add_argument("--api-key", dest="key", default=os.environ.get("EMBY_API_KEY"), help="Emby API key (or EMBY_API_KEY)")
    ap.add_argument("--map", action="append", default=[], metavar="SERVER=LOCAL",
                    help="translate a folder as Emby sees it to the folder on this computer (repeatable)")
    ap.add_argument("--all", dest="want_all", action="store_true", help="save every embedded text track, not only forced ones")
    ap.add_argument("--shows", action="store_true", help="also handle episodes")
    ap.add_argument("--dry-run", action="store_true", help="only look, write nothing")
    ap.add_argument("--limit", type=int, default=0, help="read at most this many files per run (0 = all)")
    ap.add_argument("--ffmpeg", default="ffmpeg", help="path to ffmpeg")
    ap.add_argument("--ffprobe", default="ffprobe", help="path to ffprobe")
    ap.add_argument("--silent", action="store_true",
                    help="no output, not even errors of single titles (for cron); only when Emby is not reachable")
    ap.add_argument("--retry-days", type=float, default=7, help="try files that failed again only after this many days")
    ap.add_argument("--state", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "emby_subtitles_state.json"),
                    help="state file")
    args = ap.parse_args()
    if not args.url or not args.key:
        ap.error("give the Emby address and API key (--url/--api-key or EMBY_URL/EMBY_API_KEY)")
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
            print("emby_subtitles: already running")
        return
    try:
        run(args)
    except (urllib.error.URLError, OSError, ValueError) as e:
        # the whole run failed (Emby unreachable …): one line, also with --silent
        print(f"emby_subtitles: {e}", file=sys.stderr)
        sys.exit(1)


def run(args):
    def say(msg, err=False):
        if not args.silent:
            print(msg, file=sys.stderr if err else sys.stdout)

    state = {} if args.dry_run else load_state(args.state)
    now = time.time()
    stats = {"skipped": 0, "no tracks": 0, "missing file": 0, "found": 0, "saved": 0, "failed": 0}
    read = 0

    types = ["Movie", "Episode"] if args.shows else ["Movie"]
    for it in items(args.url, args.key, types):
        if not it.get("Path") or not wanted_streams(it, args.want_all):
            continue
        name = it.get("Name", "?")
        if it.get("SeriesName"):
            name = f"{it['SeriesName']} – {name}"
        local = map_path(it["Path"], args.maps)
        try:
            st = os.stat(local)
        except OSError:
            stats["missing file"] += 1
            say(f"   not readable here: {local} (see --map)", err=True)
            continue
        entry = {"size": st.st_size, "mtime": int(st.st_mtime), "checked": now, "all": args.want_all}
        seen = state.get(local)
        if seen and seen.get("size") == st.st_size and seen.get("mtime") == int(st.st_mtime) and seen.get("all") == args.want_all:
            if not seen.get("failed") or now - seen["checked"] < args.retry_days * 86400:
                stats["skipped"] += 1
                continue
        try:
            todo = targets(local, probe_tracks(args.ffprobe, local), args.want_all)
            if not todo:
                stats["no tracks"] += 1
                state[local] = entry
                continue
            stats["found"] += 1
            names = ", ".join(os.path.basename(n) for _, n in todo)
            if args.dry_run:
                say(f"   found: {name}: {names}")
                continue
            if args.limit and read >= args.limit:
                continue
            read += 1
            extract(args.ffmpeg, local, todo)
            stats["saved"] += 1
            state[local] = entry
            say(f"   saved: {name}: {names}")
            # Emby picks the new files up when it refreshes the title (or with its next library scan)
            try:
                api(args.url, args.key, f"/Items/{it['Id']}/Refresh?MetadataRefreshMode=Default", method="POST")
            except urllib.error.URLError as e:
                say(f"   refresh of {name} failed ({e}); the next library scan finds the files", err=True)
        except (ExtractError, subprocess.SubprocessError, OSError) as e:
            stats["failed"] += 1
            state[local] = dict(entry, failed=str(e))
            say(f"   ERROR for {name}: {e}", err=True)
        if not args.dry_run:
            save_state(args.state, state)

    if not args.dry_run:
        save_state(args.state, state)
    say("== " + ", ".join(f"{k}: {v}" for k, v in stats.items()))


if __name__ == "__main__":
    main()
