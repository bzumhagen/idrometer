# Backups and restore

Nothing is ever deleted automatically. Your readings are kept forever.

## Built-in snapshots

idrometer writes a consistent copy of the database to `/var/lib/idrometer/backups/` once a day and keeps the newest 7
(`IDROMETER_BACKUP_INTERVAL`, `IDROMETER_BACKUP_KEEP`, `IDROMETER_BACKUP_DIR`). These are made with SQLite's
`VACUUM INTO` while the service runs, so they are always a clean file, which is **not** guaranteed when a
Proxmox backup copies a live database file.

Take one on demand:

```sh
idrometer-ctl backup --out /var/lib/idrometer/before-upgrade.db
```

## Proxmox backups

Include the container in a normal vzdump / Proxmox Backup Server job. Because `backups/` lives inside the container,
the last clean snapshot is always part of the container backup.

## Restore

```sh
systemctl stop idrometer
cp /var/lib/idrometer/backups/idrometer-20261030T030000Z.db /var/lib/idrometer/idrometer.db
rm -f /var/lib/idrometer/idrometer.db-wal /var/lib/idrometer/idrometer.db-shm
chown idrometer:idrometer /var/lib/idrometer/idrometer.db
systemctl start idrometer
```

Migrations run automatically when the service starts, so restoring an older snapshot into a newer version works.

## Moving to another machine

Stop the service, copy `idrometer.db` (and your `idrometer.env`) over, and start it there.
