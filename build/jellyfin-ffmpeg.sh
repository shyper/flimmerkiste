#!/bin/sh
# Installs jellyfin-ffmpeg (https://github.com/jellyfin/jellyfin-ffmpeg) from the
# Jellyfin package repository on Debian or Ubuntu and links ffmpeg and ffprobe
# into /usr/local/bin. Used by the Dockerfile and the CI jobs, so the repository
# is named in one place. jellyfin-ffmpeg brings the VAAPI drivers for Intel and
# AMD along and supports NVENC, QSV, AMF, the V4L2 encoder of the Raspberry Pi
# and fast HDR tone mapping.
set -eu
. /etc/os-release
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl gnupg
# The key is checked against the fingerprint of the Jellyfin team key
# (https://repo.jellyfin.org), so a swapped download cannot sign packages.
JELLYFIN_KEY=4918AABC486CA052358D778D49023CD01DE21A7B
curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key -o /tmp/jellyfin.key
if ! gpg --show-keys --with-colons /tmp/jellyfin.key | grep -q "^fpr:::::::::$JELLYFIN_KEY:"; then
	echo "jellyfin key: unexpected fingerprint" >&2
	exit 1
fi
gpg --dearmor --yes -o /usr/share/keyrings/jellyfin.gpg /tmp/jellyfin.key
rm -f /tmp/jellyfin.key
echo "deb [signed-by=/usr/share/keyrings/jellyfin.gpg] https://repo.jellyfin.org/$ID $VERSION_CODENAME main" \
	> /etc/apt/sources.list.d/jellyfin.list
apt-get update
# the major version is pinned on purpose: a new one changes filters and flags
JELLYFIN_FFMPEG=${JELLYFIN_FFMPEG:-jellyfin-ffmpeg7}
apt-get install -y --no-install-recommends "$JELLYFIN_FFMPEG"
ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg
ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe
