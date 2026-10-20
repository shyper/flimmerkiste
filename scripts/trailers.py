#!/usr/bin/env python3
"""Downloads trailers of new movies into a folder that Flimmerkiste plays in
its breaks like the "cinema & streaming" trailers (FLIMMERKISTE_TRAILER).

The movies come from TMDb: those now playing and coming soon in --region. For
each one a trailer is looked for in the languages of --language, in that order:
German ones at KinoCheck first, then at TMDb. yt-dlp downloads it from YouTube
as MP4 of at most --max-height lines. Next to each video a .json file holds
what Flimmerkiste shows about the movie (title, year, genres, age rating,
summary, poster, release date); a video without one is not used.

    Movie (2026) [tmdbid-123] [imdbid-tt1234567] de.mp4
    Movie (2026) [tmdbid-123] [imdbid-tt1234567] de.json

Trailers are deleted --keep-days after the movie's release. Movies without a
trailer and failed downloads are only tried again after --retry-days (state in
--state). Works the same with Plex, Jellyfin and Emby; the media server is not
asked. Titles you already have are left out by Flimmerkiste, not here.

Meant for regular runs (e.g. from cron). A second run while one is still going
exits right away. With --silent there is no output, only a line on stderr and
exit code 1 when the whole run fails (TMDb not reachable).

Needs: Python 3.9+, a TMDb API key (themoviedb.org → Settings → API), a
current yt-dlp, ffmpeg/ffprobe and deno or node for yt-dlp.

    TMDB_API_KEY=xxxx ./trailers.py --dir /mnt/media/Trailer --dry-run
    TMDB_API_KEY=xxxx ./trailers.py --dir /mnt/media/Trailer --language de,en --max-height 1080
"""

import argparse
import datetime
import fcntl
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

TMDB = "https://api.themoviedb.org/3"
KINOCHECK = "https://api.kinocheck.com"
IMAGES = "https://image.tmdb.org/t/p/"
# languages KinoCheck has trailers in that are worth asking for (German ones are
# often missing at TMDb)
KINOCHECK_LANGS = {"de"}
VIDEO_EXTS = (".mp4", ".mkv", ".webm")
# KinoCheck (behind Cloudflare) refuses Python's default User-Agent with 403
USER_AGENT = "Flimmerkiste-trailers/1.0 (+https://github.com/shyper/flimmerkiste)"


def get_json(url, headers=None):
    req = urllib.request.Request(url, headers={"Accept": "application/json", "User-Agent": USER_AGENT, **(headers or {})})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def tmdb(key, path, **params):
    return get_json(f"{TMDB}{path}?" + urllib.parse.urlencode({"api_key": key, **params}))


def movie_list(key, region, language, pages):
    """Movies now playing and coming soon in the region, without duplicates."""
    out, seen = [], set()
    for path in ("/movie/now_playing", "/movie/upcoming"):
        for page in range(1, pages + 1):
            data = tmdb(key, path, region=region, language=language, page=page)
            for m in data.get("results", []):
                if m["id"] not in seen:
                    seen.add(m["id"])
                    out.append(m)
            if page >= data.get("total_pages", 1):
                break
    return out


def region_release(details, region):
    """The release date in the region (cinema first), else the general one."""
    for r in details.get("release_dates", {}).get("results", []):
        if r.get("iso_3166_1") != region:
            continue
        dates = sorted(r.get("release_dates", []), key=lambda d: (d.get("type") not in (2, 3), d.get("release_date", "")))
        for d in dates:
            if d.get("release_date"):
                return d["release_date"][:10], next((x.get("certification") for x in dates if x.get("certification")), "")
    return details.get("release_date") or "", ""


def kinocheck_trailer(tmdb_id, lang):
    """The trailer KinoCheck has for a movie in lang: (YouTube id, name) or None."""
    try:
        data = get_json(f"{KINOCHECK}/movies?" + urllib.parse.urlencode({"tmdb_id": tmdb_id, "language": lang, "categories": "Trailer"}))
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise
    videos = ([data["trailer"]] if data.get("trailer") else []) + (data.get("videos") or [])
    for v in videos:
        if v.get("youtube_video_id") and v.get("language") == lang and "Trailer" in (v.get("categories") or []):
            return v["youtube_video_id"], v.get("title") or ""
    return None


def tmdb_trailer(details, lang):
    """The trailer TMDb lists for a movie in lang: official ones first, then the newest."""
    videos = [v for v in details.get("videos", {}).get("results", [])
              if v.get("site") == "YouTube" and v.get("type") == "Trailer" and v.get("iso_639_1") == lang and v.get("key")]
    videos.sort(key=lambda v: v.get("published_at") or "", reverse=True)
    videos.sort(key=lambda v: not v.get("official"))  # stable: the newest official one first
    return (videos[0]["key"], videos[0].get("name") or "") if videos else None


def safe_name(s):
    s = re.sub(r'[\\/:*?"<>|]+', " ", s)
    return re.sub(r"\s+", " ", s).strip().rstrip(".") or "Trailer"


class DownloadError(Exception):
    pass


def download(ytdlp, youtube_id, max_height, tmp):
    """Downloads one video as MP4 into tmp and returns its path."""
    h = int(max_height)
    # H.264 first: YouTube's MP4 is often AV1, which many Live TV apps cannot play
    # (channels in copy mode pass it on) and which is costly to decode without a GPU
    fmt = (f"bv*[height<={h}][vcodec^=avc1]+ba[ext=m4a]/b[height<={h}][vcodec^=avc1]/"
           f"bv*[height<={h}][ext=mp4]+ba[ext=m4a]/b[height<={h}][ext=mp4]/bv*[height<={h}]+ba/b[height<={h}]")
    cmd = [ytdlp, "--quiet", "--no-warnings", "--no-update", "--no-playlist", "-f", fmt,
           "--merge-output-format", "mp4", "-o", os.path.join(tmp, "trailer.%(ext)s")]
    # yt-dlp only looks for deno by itself; without a JavaScript runtime
    # YouTube refuses many downloads
    if not shutil.which("deno") and shutil.which("node"):
        cmd += ["--js-runtimes", "node"]
    r = subprocess.run(cmd + ["https://www.youtube.com/watch?v=" + youtube_id], capture_output=True, text=True, timeout=1800)
    if r.returncode != 0:
        lines = r.stderr.strip().splitlines() or [f"yt-dlp: exit code {r.returncode}"]
        raise DownloadError(lines[-1])
    files = [f for f in os.listdir(tmp) if f.startswith("trailer.") and f.endswith(VIDEO_EXTS)]
    if not files:
        raise DownloadError("yt-dlp wrote no video")
    return os.path.join(tmp, files[0])


def probe(ffprobe, path):
    """Length in milliseconds and picture height of a video."""
    r = subprocess.run([ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=height:format=duration",
                        "-of", "json", path], capture_output=True, text=True, timeout=120)
    if r.returncode != 0:
        raise DownloadError((r.stderr.strip().splitlines() or ["ffprobe failed"])[-1])
    data = json.loads(r.stdout or "{}")
    duration = float(data.get("format", {}).get("duration") or 0)
    height = int((data.get("streams") or [{}])[0].get("height") or 0)
    if duration <= 0:
        raise DownloadError("video without length")
    return int(duration * 1000), height


def descriptions(folder):
    """The .json files of the folder with their contents (unreadable ones as None);
    hidden files such as the state are not descriptions."""
    out = {}
    for name in os.listdir(folder):
        if name.endswith(".json") and not name.startswith("."):
            path = os.path.join(folder, name)
            try:
                with open(path) as f:
                    out[path] = json.load(f)
            except (OSError, ValueError):
                out[path] = None
    return out


def cleanup(folder, keep_days, today, dry_run, say):
    """Deletes trailers keep_days after the release, and leftovers of broken runs."""
    removed = 0
    limit = (today - datetime.timedelta(days=keep_days)).isoformat()
    known = set()
    for path, d in descriptions(folder).items():
        if not isinstance(d, dict):
            continue
        video = os.path.join(folder, os.path.basename(d.get("video") or ""))
        known.add(video)
        released = d.get("released") or d.get("downloaded") or ""
        if released and released < limit:
            say(f"   deleted: {d.get('title', '?')} (released {released})")
            removed += 1
            if not dry_run:
                for p in (video, path):
                    if os.path.isfile(p):
                        os.remove(p)
    # videos of this script without a description (a run that broke off) and partial files
    old = time.time() - 86400
    for name in os.listdir(folder):
        path = os.path.join(folder, name)
        ours = "[tmdbid-" in name and name.endswith(VIDEO_EXTS) and path not in known
        if (ours or name.endswith(".part")) and os.path.isfile(path) and os.path.getmtime(path) < old:
            removed += 1
            if not dry_run:
                os.remove(path)
    return removed


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


def write_json(path, data):
    tmp = path + ".part"
    with open(tmp, "w") as f:
        json.dump(data, f, ensure_ascii=False, indent=1)
    os.replace(tmp, path)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dir", default=os.environ.get("TRAILER_DIR"), help="trailer folder (or TRAILER_DIR)")
    ap.add_argument("--tmdb-key", default=os.environ.get("TMDB_API_KEY"), help="TMDb API key (or TMDB_API_KEY)")
    ap.add_argument("--language", default="de", help="trailer languages in order of preference, e.g. de,en (default de)")
    ap.add_argument("--region", default="DE", help="country of the cinema releases and age ratings (default DE)")
    ap.add_argument("--max-height", type=int, default=1080, help="highest picture height to download, e.g. 720 (default 1080)")
    ap.add_argument("--keep-days", type=int, default=60, help="delete trailers this many days after the release (default 60)")
    ap.add_argument("--pages", type=int, default=2, help="pages of 20 movies read from each TMDb list (default 2)")
    ap.add_argument("--limit", type=int, default=0, help="at most this many downloads per run (0 = all)")
    ap.add_argument("--retry-days", type=float, default=3, help="look again for movies without a trailer, or after a failed download, after this many days")
    ap.add_argument("--dry-run", action="store_true", help="only look, download and delete nothing")
    ap.add_argument("--silent", action="store_true", help="no output (for cron); only when the whole run fails")
    ap.add_argument("--yt-dlp", dest="ytdlp", default="yt-dlp", help="path to yt-dlp")
    ap.add_argument("--ffprobe", default="ffprobe", help="path to ffprobe")
    ap.add_argument("--state", default=None, help="state file (default: .trailers_state.json in the trailer folder)")
    args = ap.parse_args()
    if not args.dir or not args.tmdb_key:
        ap.error("give the trailer folder and a TMDb API key (--dir/--tmdb-key or TRAILER_DIR/TMDB_API_KEY)")
    args.languages = [l.strip().lower()[:2] for l in args.language.split(",") if l.strip()]
    if not args.languages:
        ap.error("--language needs at least one language")
    if args.max_height < 144:
        ap.error("--max-height is too small")
    args.region = args.region.upper()
    if not os.path.isdir(args.dir):
        ap.error(f"the trailer folder {args.dir} does not exist")
    args.state = args.state or os.path.join(args.dir, ".trailers_state.json")

    lock = open(args.state + ".lock", "w")
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        if not args.silent:
            print("trailers: already running")
        return
    try:
        run(args)
    except (urllib.error.URLError, OSError, ValueError) as e:
        # the whole run failed (TMDb unreachable, wrong key …): one line, also with --silent
        print(f"trailers: {e}", file=sys.stderr)
        sys.exit(1)


def run(args, today=None):
    def say(msg, err=False):
        if not args.silent:
            print(msg, file=sys.stderr if err else sys.stdout)

    today = today or datetime.date.today()
    now = time.time()
    stats = {"movies": 0, "present": 0, "released long ago": 0, "no trailer": 0, "skipped": 0, "found": 0, "downloaded": 0,
             "failed": 0, "deleted": 0}
    state = {} if args.dry_run else load_state(args.state)
    stats["deleted"] = cleanup(args.dir, args.keep_days, today, args.dry_run, say)

    have = {d.get("tmdb") for d in descriptions(args.dir).values() if isinstance(d, dict)}
    meta_lang = f"{args.languages[0]}-{args.region}"
    oldest = (today - datetime.timedelta(days=args.keep_days)).isoformat()
    for m in movie_list(args.tmdb_key, args.region, meta_lang, args.pages):
        stats["movies"] += 1
        if m["id"] in have:
            stats["present"] += 1
            continue
        seen = state.get(str(m["id"]))
        if seen and now - seen["checked"] < args.retry_days * 86400:
            stats["skipped"] += 1
            continue
        try:
            d = tmdb(args.tmdb_key, f"/movie/{m['id']}", language=meta_lang, append_to_response="release_dates,external_ids,videos",
                     include_video_language=",".join(args.languages))
        except urllib.error.HTTPError as e:
            say(f"   ERROR for {m.get('title', '?')}: TMDb {e.code}", err=True)
            continue
        released, rating = region_release(d, args.region)
        if released and released < oldest:
            stats["released long ago"] += 1  # would be deleted right away
            continue
        pick = None
        for lang in args.languages:
            if lang in KINOCHECK_LANGS:
                try:
                    pick = kinocheck_trailer(m["id"], lang)
                except (urllib.error.URLError, ValueError) as e:
                    say(f"   KinoCheck not reachable for {d.get('title', '?')}: {e}", err=True)
            pick = pick or tmdb_trailer(d, lang)
            if pick:
                pick = (*pick, lang)
                break
        title = d.get("title") or m.get("title") or "?"
        year = int((released or d.get("release_date") or "0")[:4] or 0)
        if not pick:
            stats["no trailer"] += 1
            state[str(m["id"])] = {"checked": now}
            continue
        youtube, name, lang = pick
        stats["found"] += 1
        imdb = (d.get("external_ids") or {}).get("imdb_id") or d.get("imdb_id") or ""
        base = safe_name(f"{title} ({year})" if year else title) + f" [tmdbid-{m['id']}]" + (f" [imdbid-{imdb}]" if imdb else "") + f" {lang}"
        if args.dry_run:
            say(f"   found: {base} ({name or youtube})")
            continue
        if args.limit and stats["downloaded"] + stats["failed"] >= args.limit:
            continue
        try:
            with tempfile.TemporaryDirectory(dir=args.dir, prefix=".dl-") as tmp:
                path = download(args.ytdlp, youtube, args.max_height, tmp)
                duration, height = probe(args.ffprobe, path)
                video = base + os.path.splitext(path)[1]
                os.replace(path, os.path.join(args.dir, video))
            if rating and args.region == "DE":
                rating = "de/" + rating
            poster, backdrop = d.get("poster_path"), d.get("backdrop_path")
            write_json(os.path.join(args.dir, base + ".json"), {
                "video": video, "kind": "movie", "tmdb": m["id"], "imdb": imdb, "title": title, "year": year,
                "released": released, "genres": [g["name"] for g in d.get("genres", []) if g.get("name")],
                "content_rating": rating, "summary": d.get("overview") or "",
                "poster": IMAGES + "w500" + poster if poster else "", "backdrop": IMAGES + "w1280" + backdrop if backdrop else "",
                "runtime_min": d.get("runtime") or 0, "language": lang, "name": name, "youtube": youtube,
                "height": height, "duration_ms": duration, "downloaded": today.isoformat(),
            })
            state.pop(str(m["id"]), None)
            stats["downloaded"] += 1
            say(f"   downloaded: {video}")
        except (DownloadError, subprocess.SubprocessError, OSError) as e:
            stats["failed"] += 1
            state[str(m["id"])] = {"checked": now, "failed": youtube}
            say(f"   ERROR for {title}: {e}", err=True)
        save_state(args.state, state)

    if not args.dry_run:
        save_state(args.state, state)
    say("== " + ", ".join(f"{k}: {v}" for k, v in stats.items()))


if __name__ == "__main__":
    main()
