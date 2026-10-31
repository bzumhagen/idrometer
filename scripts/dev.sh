#!/usr/bin/env bash
# One-command local playground: builds, sets an admin password, starts the
# server, and feeds it a simulated fermentation. Ctrl-C stops everything.
# Every setting can be overridden from the environment (see defaults below).
set -euo pipefail
cd "$(dirname "$0")/.."

: "${DEV_DIR:=.dev}"                                  # database and backups live here
: "${IDROMETER_LISTEN:=:8080}"
: "${IDROMETER_INGEST_TOKEN:=dev-token-0123456789}"
explicit_password="${DEV_PASSWORD:-}"
: "${DEV_PASSWORD:=idrometer-dev}"                        # admin password
: "${DEV_SIMULATE:=true}"                             # false = start empty
: "${DEV_DAYS:=6}"                                    # days of simulated history (15-minute readings)
: "${DEV_LIVE:=false}"                                # true = keep posting new readings while running
: "${DEV_INTERVAL:=5s}"                               # delay between live readings
: "${DEV_RESET:=false}"                               # true = wipe the database first

# The playground owns its data: ignore server settings left in your shell (for
# example from the manual steps in the README), or it would open that data.
for v in IDROMETER_DB_PATH IDROMETER_BACKUP_DIR IDROMETER_SECURE_COOKIES IDROMETER_BACKUP_INTERVAL \
  IDROMETER_BACKUP_KEEP IDROMETER_SWEEP_INTERVAL IDROMETER_LOG_LEVEL; do
  if [ -n "${!v:-}" ]; then
    echo "dev: ignoring $v=${!v} from your environment (use DEV_DB to choose the database)" >&2
    unset "$v"
  fi
done

port="${IDROMETER_LISTEN##*:}"
base="http://localhost:$port"

# Fail before touching any data if something already answers on the port (a
# leftover server would also fool the health check below).
if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
  echo "dev: port $port is already in use:" >&2
  (ss -ltnp 2>/dev/null | grep -E ":$port " || true) >&2
  echo "Stop that process, or pick another port: task dev IDROMETER_LISTEN=:$((port + 1))" >&2
  exit 1
fi

[ "$DEV_RESET" = true ] && rm -rf "$DEV_DIR"
mkdir -p "$DEV_DIR"
export IDROMETER_LISTEN IDROMETER_INGEST_TOKEN
export IDROMETER_DB_PATH="${DEV_DB:-$DEV_DIR/idrometer.db}"
export IDROMETER_BACKUP_DIR="$DEV_DIR/backups"

fresh=false
[ -f "$IDROMETER_DB_PATH" ] || fresh=true


go build -o "$DEV_DIR/idrometer" ./cmd/idrometer
go build -o "$DEV_DIR/simulate" ./cmd/simulate

# Only (re)set the password for a new database or when asked, so restarting
# does not sign you out.
if [ ! -f "$IDROMETER_DB_PATH" ] || [ -n "$explicit_password" ]; then
  printf '%s\n' "$DEV_PASSWORD" | "$DEV_DIR/idrometer" admin set-password >/dev/null
fi

# A new database gets a realistic fermentation backfilled (before the server starts).
if [ "$DEV_SIMULATE" = true ] && [ "$fresh" = true ]; then
  "$DEV_DIR/simulate" -seed-db "$IDROMETER_DB_PATH" -days "$DEV_DAYS"
fi

"$DEV_DIR/idrometer" serve &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true' EXIT INT TERM

for _ in $(seq 1 50); do
  curl -fsS "$base/healthz" >/dev/null 2>&1 && break
  kill -0 "$server" 2>/dev/null || { echo "server exited" >&2; exit 1; }
  sleep 0.2
done

cat <<MSG

  idrometer is running:  $base
  admin password:        $DEV_PASSWORD   (sign in to edit; unchanged on restart unless DEV_PASSWORD is given)
  ingest token:          $IDROMETER_INGEST_TOKEN
  database:              $IDROMETER_DB_PATH   (DEV_RESET=true starts clean)

MSG

if [ "$DEV_SIMULATE" = true ] && [ "$DEV_LIVE" = true ]; then
  echo "  posting live readings every $DEV_INTERVAL (each advances the curve 15 minutes). Ctrl-C to stop."
  "$DEV_DIR/simulate" -url "$base/api/v1/ingest/ispindel" -token "$IDROMETER_INGEST_TOKEN" \
    -start-day "$DEV_DAYS" -count 100000 -interval "$DEV_INTERVAL"
fi
wait "$server"
