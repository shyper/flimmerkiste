#!/bin/sh
# Generates the small test library the Jellyfin and Emby test servers read:
# three movies (two audio tracks, forced subtitles, 16:9 / 2.39:1 / 4:3), a
# show with two seasons, a local trailer and a theme song. Metadata comes from
# NFO files, so the servers need no internet and results are reproducible.
#
#   test/mediaserver/make-media.sh [target dir]   (default: test/mediaserver/media)
set -eu
OUT=${1:-"$(dirname "$0")/media"}
SECS=${SECS:-90}
command -v ffmpeg >/dev/null || { echo "ffmpeg is needed" >&2; exit 1; }

video() { # file, size, title
	ffmpeg -hide_banner -loglevel error -y \
		-f lavfi -i "testsrc2=size=$2:rate=25:duration=$SECS" \
		-f lavfi -i "sine=frequency=440:duration=$SECS" \
		-f lavfi -i "sine=frequency=660:duration=$SECS" \
		-map 0:v -map 1:a -map 2:a \
		-c:v libx264 -preset ultrafast -crf 35 -g 50 -c:a aac -b:a 64k -ac 2 \
		-metadata title="$3" \
		-metadata:s:a:0 language=deu -metadata:s:a:0 title="Deutsch" \
		-metadata:s:a:1 language=eng -metadata:s:a:1 title="English" \
		-disposition:a:0 default "$1"
}

srt() { # file, text
	printf '1\n00:00:05,000 --> 00:00:09,000\n%s\n\n2\n00:00:20,000 --> 00:00:24,000\n%s\n' "$2" "$2" > "$1"
}

movie() { # dir name, size, nfo body
	d="$OUT/movies/$1"
	mkdir -p "$d"
	video "$d/$1.mkv" "$2" "$1"
	printf '<?xml version="1.0" encoding="UTF-8"?>\n<movie>\n%s\n</movie>\n' "$3" > "$d/movie.nfo"
}

movie "Matrix (1999)" 640x360 '<title>Matrix</title><year>1999</year><premiered>1999-03-31</premiered>
<plot>Ein Hacker erfährt, dass seine Welt eine Simulation ist.</plot>
<genre>Action</genre><genre>Science-Fiction</genre><tag>Kult</tag><set><name>Matrix Filmreihe</name></set>
<mpaa>FSK-16</mpaa><rating>8.7</rating><criticrating>83</criticrating><studio>Warner Bros.</studio><country>USA</country>
<director>Lana Wachowski</director><credits>Lilly Wachowski</credits><actor><name>Keanu Reeves</name><role>Neo</role></actor>
<uniqueid type="tmdb" default="true">603</uniqueid><uniqueid type="imdb">tt0133093</uniqueid>'
srt "$OUT/movies/Matrix (1999)/Matrix (1999).de.forced.srt" "Erzwungener Untertitel"
# local trailer and theme song
ffmpeg -hide_banner -loglevel error -y -f lavfi -i "testsrc=size=640x360:rate=25:duration=20" -f lavfi -i "sine=frequency=880:duration=20" \
	-c:v libx264 -preset ultrafast -crf 35 -c:a aac -b:a 64k -metadata:s:a:0 language=deu "$OUT/movies/Matrix (1999)/Matrix (1999)-trailer.mkv"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i "sine=frequency=523:duration=15" -c:a libmp3lame -b:a 64k "$OUT/movies/Matrix (1999)/theme.mp3"

movie "Ghostbusters (1984)" 960x400 '<title>Ghostbusters</title><year>1984</year><premiered>1984-06-08</premiered>
<plot>Drei Wissenschaftler gründen eine Geisterjagd-Firma.</plot>
<genre>Komödie</genre><genre>Fantasy</genre><mpaa>FSK-12</mpaa><rating>7.8</rating><country>USA</country>
<uniqueid type="tmdb" default="true">620</uniqueid><uniqueid type="imdb">tt0087332</uniqueid>'
# embedded text subtitles: a forced German track and a full English one (for the *_subtitles.py scripts)
gb="$OUT/movies/Ghostbusters (1984)/Ghostbusters (1984).mkv"
srt "$OUT/gb-forced.srt" "Eingebetteter erzwungener Untertitel"
srt "$OUT/gb-full.srt" "Embedded full subtitle"
ffmpeg -hide_banner -loglevel error -y -i "$gb" -i "$OUT/gb-forced.srt" -i "$OUT/gb-full.srt" -map 0 -map 1 -map 2 -c copy \
	-metadata:s:s:0 language=deu -disposition:s:0 forced -metadata:s:s:1 language=eng -disposition:s:1 0 "$gb.tmp.mkv"
mv "$gb.tmp.mkv" "$gb"
rm "$OUT/gb-forced.srt" "$OUT/gb-full.srt"

movie "Casablanca (1942)" 640x480 '<title>Casablanca</title><year>1942</year><premiered>1942-11-26</premiered>
<plot>Ein Barbesitzer in Marokko trifft seine große Liebe wieder.</plot>
<genre>Drama</genre><genre>Liebesfilm</genre><mpaa>FSK-12</mpaa><rating>8.5</rating>
<uniqueid type="tmdb" default="true">289</uniqueid>'

show="$OUT/shows/Testserie"
mkdir -p "$show"
printf '<?xml version="1.0" encoding="UTF-8"?>\n<tvshow><title>Testserie</title><year>2001</year><premiered>2001-01-10</premiered>
<plot>Eine Serie nur für Tests.</plot><genre>Komödie</genre><mpaa>FSK-6</mpaa><rating>7.1</rating><tag>Sitcom</tag>
<uniqueid type="tvdb" default="true">71663</uniqueid><uniqueid type="tmdb">456</uniqueid></tvshow>\n' > "$show/tvshow.nfo"
for s in 1 2; do
	mkdir -p "$show/Season 0$s"
	for e in 1 2; do
		f="$show/Season 0$s/Testserie - S0${s}E0$e"
		video "$f.mkv" 640x360 "Folge $s.$e"
		printf '<?xml version="1.0" encoding="UTF-8"?>\n<episodedetails><title>Folge %s.%s</title><season>%s</season><episode>%s</episode><aired>2001-0%s-0%s</aired><uniqueid type="tvdb" default="true">%s</uniqueid></episodedetails>\n' \
			"$s" "$e" "$s" "$e" "$s" "$e" "55$s$e" > "$f.nfo"
	done
done
echo "test media in $OUT"
