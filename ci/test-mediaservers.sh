#!/bin/sh
# Integration tests against real Jellyfin and Emby servers in containers
# (test/mediaserver). Needs docker with compose and ffmpeg; the media is generated,
# so no internet is needed beyond the server images.
set -eu
cd test/mediaserver
./make-media.sh
docker compose -p flimmerkiste-test up -d
trap 'docker compose -p flimmerkiste-test logs --tail=50; docker compose -p flimmerkiste-test down -v' EXIT
cd ../..
# the servers need a moment after the start
for port in 18096 18097; do
	for i in $(seq 1 60); do
		curl -fsS -o /dev/null "http://127.0.0.1:$port/System/Info/Public" && break
		sleep 2
	done
done
FLIMMERKISTE_MEDIASERVER_E2E=all go test -count=1 -v ./internal/jellyfin/
