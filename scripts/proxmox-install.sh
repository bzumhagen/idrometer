#!/usr/bin/env bash
# Create an unprivileged LXC container on this Proxmox VE host and install
# idrometer in it. Run as root on the Proxmox host.
#
#   ./proxmox-install.sh                 create a new container
#   ./proxmox-install.sh update 105      upgrade idrometer inside container 105
#
# Settings (environment variables, all optional):
#   CTID      container id               (default: next free id)
#   CT_HOSTNAME  container hostname      (default: idrometer)
#   STORAGE   storage for the disk       (default: first storage that holds containers)
#   DISK_GB   disk size in GB            (default: 2)
#   MEMORY    memory in MB               (default: 256)
#   CORES     CPU cores                  (default: 1)
#   BRIDGE    network bridge             (default: vmbr0)
#   IP        "dhcp" or CIDR e.g. 192.168.1.50/24,gw=192.168.1.1 (default: dhcp)
#   CONSOLE_AUTOLOGIN  1 logs root in automatically on the Proxmox console, 0 keeps the login prompt (default: 1)
#   FORCE     1 reinstalls the same version or allows an older one, with update (default: 0)
#   IDROMETER_VERSION / IDROMETER_REPO   passed to lxc-install.sh
set -euo pipefail

repo="${IDROMETER_REPO:-bzumhagen/idrometer}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

die() { echo "proxmox-install: $*" >&2; exit 1; }
info() { echo "==> $*"; }

[ "$(id -u)" -eq 0 ] || die "run as root on the Proxmox host"
command -v pct >/dev/null || die "pct not found: this must run on a Proxmox VE host"

# Run lxc-install.sh inside the container: the local copy if this script was run
# from a checkout, otherwise the one attached to the release.
install_in_ct() {
    local ctid="$1"
    if [ -f "${here}/lxc-install.sh" ]; then
        pct push "$ctid" "${here}/lxc-install.sh" /root/lxc-install.sh --perms 0700
    else
        pct exec "$ctid" -- bash -c \
            "apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null && \
             curl -fsSL -o /root/lxc-install.sh https://github.com/${repo}/releases/latest/download/lxc-install.sh && \
             chmod 0700 /root/lxc-install.sh"
    fi
    pct exec "$ctid" -- env \
        "IDROMETER_REPO=${repo}" "IDROMETER_VERSION=${IDROMETER_VERSION:-latest}" "FORCE=${FORCE:-0}" \
        /root/lxc-install.sh
}

if [ "${1:-}" = "update" ]; then
    ctid="${2:-}"
    [ -n "$ctid" ] || die "usage: $0 update <ctid>"
    pct status "$ctid" | grep -q running || die "container $ctid is not running"
    install_in_ct "$ctid"
    exit 0
fi

ctid="${CTID:-$(pvesh get /cluster/nextid)}"
hostname="${CT_HOSTNAME:-idrometer}"
disk="${DISK_GB:-2}"
memory="${MEMORY:-256}"
cores="${CORES:-1}"
bridge="${BRIDGE:-vmbr0}"
ip="${IP:-dhcp}"

storage="${STORAGE:-}"
if [ -z "$storage" ]; then
    storage="$(pvesm status --content rootdir | awk 'NR==2 {print $1}')"
    [ -n "$storage" ] || die "no storage that can hold containers; set STORAGE"
fi

info "Looking for a Debian 13 template"
pveam update >/dev/null
# pveam lists every architecture; a template for another one fails to start ("Exec format error").
arch="$(dpkg --print-architecture)"
template="$(pveam available --section system | awk '{print $2}' | grep "^debian-13-standard.*_${arch}\." | sort -V | tail -n1)"
[ -n "$template" ] || die "no debian-13 ${arch} template available from pveam"
tmpl_storage="$(pvesm status --content vztmpl | awk 'NR==2 {print $1}')"
[ -n "$tmpl_storage" ] || die "no storage for container templates"
if ! pveam list "$tmpl_storage" | grep -q "$template"; then
    info "Downloading $template"
    pveam download "$tmpl_storage" "$template"
fi

if [ "$ip" = "dhcp" ]; then
    net="name=eth0,bridge=${bridge},ip=dhcp"
else
    net="name=eth0,bridge=${bridge},ip=${ip}"
fi

info "Creating unprivileged container ${ctid} (${hostname}) on ${storage}"
pct create "$ctid" "${tmpl_storage}:vztmpl/${template}" \
    --hostname "$hostname" \
    --unprivileged 1 \
    --features nesting=1 \
    --cores "$cores" --memory "$memory" --swap 0 \
    --rootfs "${storage}:${disk}" \
    --net0 "$net" \
    --onboot 1 \
    --ostype debian

info "Starting container"
pct start "$ctid"
for _ in $(seq 1 30); do
    pct exec "$ctid" -- getent hosts deb.debian.org >/dev/null 2>&1 && break
    sleep 2
done
pct exec "$ctid" -- getent hosts deb.debian.org >/dev/null 2>&1 \
    || die "the container has no network; check the bridge/IP settings, then run: $0 update ${ctid}"

if [ "${CONSOLE_AUTOLOGIN:-1}" = "1" ]; then
    # The template's root has no password, so the console prompt can't be passed. Anyone who can open
    # the console already has root on this container through Proxmox (pct enter).
    info "Enabling automatic login on the container console"
    # shellcheck disable=SC2016  # the script runs in the container; nothing should expand here
    pct exec "$ctid" -- bash -c 'mkdir -p /etc/systemd/system/container-getty@1.service.d && cat > /etc/systemd/system/container-getty@1.service.d/autologin.conf <<EOF
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear --keep-baud tty%I 115200,38400,9600 \$TERM
EOF
systemctl daemon-reload && systemctl restart container-getty@1.service'
fi

info "Installing idrometer"
install_in_ct "$ctid"

echo
echo "Choose the admin password for the web UI (used for editing; viewing needs no login)."
while :; do
    read -r -s -p "Password (10+ characters): " pw1; echo
    read -r -s -p "Repeat: " pw2; echo
    if [ "$pw1" != "$pw2" ]; then echo "Passwords do not match."; continue; fi
    if printf '%s\n' "$pw1" | pct exec "$ctid" -- idrometer-ctl admin set-password; then break; fi
done
unset pw1 pw2

addr="$(pct exec "$ctid" -- hostname -I 2>/dev/null | awk '{print $1}')"
token="$(pct exec "$ctid" -- sed -n 's/^IDROMETER_INGEST_TOKEN=//p' /etc/idrometer/idrometer.env)"
cat <<DONE

idrometer is running in container ${ctid}.

  Web UI:  http://${addr:-<container-ip>}:8080

  iSpindel configuration (Configuration > Service Type: HTTP):
    Server Address: ${addr:-<container-ip>}
    Server Port:    8080
    Path/URL:       /api/v1/ingest/ispindel
    Token:          ${token}

  The iSpindel only speaks plain HTTP, so keep this on your LAN.
  Upgrade later with:  $0 update ${ctid}
DONE
