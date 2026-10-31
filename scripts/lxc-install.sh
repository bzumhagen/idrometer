#!/usr/bin/env bash
# Install or upgrade idrometer inside a Debian/Ubuntu container or VM.
# Run as root. Downloads the .deb from GitHub Releases and verifies its checksum.
#
#   IDROMETER_VERSION=v0.1.0 ./lxc-install.sh     # default: the latest release
#   IDROMETER_REPO=owner/repo ./lxc-install.sh    # default: bzumhagen/idrometer
#   FORCE=1 ./lxc-install.sh                      # reinstall the same version, or go back to an older one
set -euo pipefail

repo="${IDROMETER_REPO:-bzumhagen/idrometer}"
version="${IDROMETER_VERSION:-latest}"

die() { echo "lxc-install: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root"
command -v apt-get >/dev/null || die "this script needs a Debian-based system (apt-get)"

arch="$(dpkg --print-architecture)"
case "$arch" in
    amd64 | arm64) ;;
    *) die "unsupported architecture: $arch (releases are built for amd64 and arm64)" ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl adduser >/dev/null

if [ "$version" = "latest" ]; then
    version="$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
    [ -n "$version" ] || die "could not determine the latest release of ${repo}"
fi
number="${version#v}"
base="https://github.com/${repo}/releases/download/${version}"
deb="idrometer_${number}_linux_${arch}.deb"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
echo "Downloading idrometer ${version} (${arch})..."
curl -fsSL -o "${work}/${deb}" "${base}/${deb}" || die "download failed: ${base}/${deb}"
curl -fsSL -o "${work}/checksums.txt" "${base}/checksums.txt" || die "could not download checksums.txt"

echo "Verifying checksum..."
(cd "$work" && grep -F " ${deb}" checksums.txt | sha256sum --check --status) \
    || die "checksum mismatch for ${deb}; refusing to install"

if [ "${FORCE:-0}" = "1" ]; then
    apt-get install -y -qq --reinstall --allow-downgrades "${work}/${deb}"
else
    apt-get install -y -qq "${work}/${deb}"
fi

echo
echo "idrometer ${version} is installed and running."
echo "  Set the admin password:   idrometer-ctl admin set-password"
echo "  Config file:              /etc/idrometer/idrometer.env"
echo "  Logs:                     journalctl -u idrometer -f"
