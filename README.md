<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/idrometer-wordmark-white.svg">
    <img src="assets/logo/idrometer-wordmark.svg" alt="idrometer" height="72">
  </picture>
</p>

A small, self-hosted service that receives data from an [iSpindel](https://www.ispindel.de/) hydrometer during
fermentation, stores it, and graphs it. One device can be used for many fermentations in a row: readings are grouped
into a fermentation automatically, and a new one starts after a long gap in reporting or once you close the current one.

Designed to be light: one static binary (~14 MB), SQLite, no Node, no external services. Runs happily in a
256 MB unprivileged Proxmox LXC.

## Features

- **Automatic sessions.** A reading after a long gap (default 72 h, configurable globally and per device) or after you
  complete a fermentation starts a new one. Silent devices are closed automatically.
- **Live graph** of gravity and temperature, optional battery and signal strength, hand measurements (OG/FG) and
  timeline events (pitch, dry hop, cold crash…). Updates as readings arrive.
- **Fix mistakes:** split, merge, reopen, exclude readings (drag across the graph), soft-delete and restore.
- **Recipes** you can link batches to, and a **compare** view that overlays batches by days since start.
- **Derived figures:** OG, current gravity, apparent attenuation, ABV estimate, change per day.
- **Calibration** per device, applied to past readings too. SG or °Plato, °C or °F.
- **Export** to CSV or JSON. **Scheduled backups** of the database.
- **Read-only without a login**; editing needs the admin password.

## Install on Proxmox

```sh
# on the Proxmox host, as root
curl -fsSLO https://github.com/bzumhagen/idrometer/releases/latest/download/proxmox-install.sh
bash proxmox-install.sh
```

Details, upgrades and security notes: [docs/install.md](docs/install.md).
Then point your iSpindel at it: [docs/ispindel.md](docs/ispindel.md).

## Try it locally

With [Task](https://taskfile.dev) and Go installed:

```sh
task dev
```

This builds, sets an admin password, starts the server with a simulated six-day fermentation (a reading every 15 minutes), then keeps running until
Ctrl-C. Open <http://localhost:8080> and sign in with the password it prints (`idrometer-dev` by default). Data lives in
`.dev/`. Override anything from the environment, e.g.
`task dev DEV_PASSWORD=hunter2-hunter2 DEV_RESET=true DEV_SIMULATE=false IDROMETER_LISTEN=:9090` (also
`DEV_DAYS`, `DEV_LIVE=true` to keep posting new readings, `DEV_INTERVAL`, `IDROMETER_INGEST_TOKEN`). Without Task, run `scripts/dev.sh` directly.

Or by hand:

```sh
export IDROMETER_INGEST_TOKEN=$(head -c 24 /dev/urandom | base64 | tr -d '/+=')
export IDROMETER_DB_PATH=./idrometer.db
go run ./cmd/idrometer serve &
printf 'choose-a-password\n' | go run ./cmd/idrometer admin set-password
go run ./cmd/simulate -token "$IDROMETER_INGEST_TOKEN" -start-day 0 -interval 1s   # live fake iSpindel
```

Then open <http://localhost:8080>.

`task` lists everything else: `serve`, `build`, `test`, `lint`, `vuln`, `check` (all of CI), `backup`, `snapshot`.

## Configuration

Environment variables (in `/etc/idrometer/idrometer.env` for the packaged service):

| Variable | Default | Meaning |
|---|---|---|
| `IDROMETER_LISTEN` | `:8080` | listen address |
| `IDROMETER_DB_PATH` | `idrometer.db` | SQLite database file |
| `IDROMETER_INGEST_TOKEN` | required, 16+ chars | token devices must present |
| `IDROMETER_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `IDROMETER_SECURE_COOKIES` | `false` | set when the UI is served over HTTPS |
| `IDROMETER_SWEEP_INTERVAL` | `15m` | how often silent fermentations are closed |
| `IDROMETER_BACKUP_INTERVAL` | `24h` | snapshot interval (`1h` or more, or `off`) |
| `IDROMETER_BACKUP_KEEP` | `7` | snapshots to keep |
| `IDROMETER_BACKUP_DIR` | `backups/` beside the database | where snapshots go |

Commands: `idrometer serve`, `idrometer admin set-password`, `idrometer backup --out FILE`, `idrometer version`.
See [docs/backup-restore.md](docs/backup-restore.md) and [docs/calibration.md](docs/calibration.md).

## API

Read-only and public, like the UI: `GET /api/v1/fermentations`, `/api/v1/fermentations/{id}/series`,
`/api/v1/fermentations/{id}/export.csv` and `.json`. Device ingest: `POST /api/v1/ingest/ispindel`.
`GET /healthz` for monitoring.

## Architecture

A semi-onion layout with the domain at the centre and dependencies pointing inward, enforced in CI.
See [docs/architecture.md](docs/architecture.md) for how to add another hydrometer type or a Postgres store.

## License

MIT
