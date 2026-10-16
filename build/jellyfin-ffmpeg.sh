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
curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key | gpg --dearmor --yes -o /usr/share/keyrings/jellyfin.gpg
echo "deb [signed-by=/usr/share/keyrings/jellyfin.gpg] https://repo.jellyfin.org/$ID $VERSION_CODENAME main" \
	> /etc/apt/sources.list.d/jellyfin.list
apt-get update
apt-get install -y --no-install-recommends jellyfin-ffmpeg7
ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg
ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe
