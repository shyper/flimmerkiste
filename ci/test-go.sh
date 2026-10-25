#!/bin/sh
# The Go tests including the end-to-end test (fake Plex, real ffmpeg, MPEG-TS
# streams) and the tests of the helper scripts. Needs ffmpeg (build/jellyfin-ffmpeg.sh)
# and python3. Two packages at a time: the stream tests measure timing.
# COVERAGE=1 also writes coverage.out and prints the total.
set -eu
export FLIMMERKISTE_E2E=1
if [ "${COVERAGE:-}" = 1 ]; then
	go test -p 2 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1
else
	go test -p 2 ./...
fi
python3 -m unittest discover -s scripts -p 'test_*.py'
