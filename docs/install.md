# Installing on Proxmox

idrometer runs well in a small **unprivileged LXC container** (1 core, 256 MB RAM, 2 GB disk is plenty).

## One command (recommended)

On the Proxmox host, as root:

```sh
curl -fsSLO https://github.com/bzumhagen/idrometer/releases/latest/download/proxmox-install.sh
less proxmox-install.sh        # read it first
bash proxmox-install.sh
```

It creates a Debian 13 container, installs the latest release (the `.deb` is checksum-verified), asks you for an
admin password, and prints the settings to type into your iSpindel.

Options are environment variables, for example
`CTID=210 STORAGE=local-zfs DISK_GB=4 IP=192.168.1.50/24,gw=192.168.1.1 bash proxmox-install.sh`.
See the top of the script for the full list.

Upgrade later (the data and your settings are kept):

```sh
bash proxmox-install.sh update 210
```

## Into a container or VM you already have

Inside a Debian or Ubuntu system, as root:

```sh
curl -fsSL https://github.com/bzumhagen/idrometer/releases/latest/download/lxc-install.sh | bash
idrometer-ctl admin set-password
```

Or install a downloaded `.deb` by hand: `apt install ./idrometer_<version>_linux_<arch>.deb`.

## What gets installed

| Path | What |
|---|---|
| `/usr/bin/idrometer` | the server (one static binary, amd64 or arm64) |
| `/usr/bin/idrometer-ctl` | runs idrometer commands as the service user, with the service's settings |
| `/etc/idrometer/idrometer.env` | your configuration (created once, with a random ingest token; never overwritten) |
| `/var/lib/idrometer/` | database and `backups/` (kept when you remove the package) |
| `idrometer.service` | systemd unit, hardened (`systemd-analyze security idrometer`) |

Day to day:

```sh
systemctl status idrometer
journalctl -u idrometer -f
idrometer-ctl admin set-password      # change the admin password (signs everyone out)
idrometer-ctl backup --out /var/lib/idrometer/before-upgrade.db
```

Always use `idrometer-ctl` rather than running `idrometer` as root: root-owned database files would stop the service from writing.

The container needs the **nesting** feature (the install script sets it) for systemd's sandboxing options to work in an unprivileged container.

## Security notes

- **The iSpindel only speaks plain HTTP.** Keep the ingest endpoint on your LAN. Don't expose port 8080 to the internet.
- Viewing needs no login, so anyone on your network can read your fermentation data. Editing needs the admin password.
- To put the web UI behind HTTPS, use a reverse proxy (Caddy, nginx, Traefik) and set `IDROMETER_SECURE_COOKIES=true`.
  Proxy only the UI paths if you want ingest to stay plain HTTP on the LAN. The proxy must not buffer `text/event-stream`
  responses (nginx: `proxy_buffering off;`, idrometer already sends `X-Accel-Buffering: no`).
- The login is rate-limited per client address. Behind a proxy every visitor shares the proxy's address.
