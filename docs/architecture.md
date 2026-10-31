# Architecture

idrometer is a single Go binary with a semi-onion layout. **Dependencies point inward**: adapters know about the domain,
the domain knows about nothing.

```
 iSpindel ──►  api/ingest ─┐                         ┌─► store/sqlite   (domain ports: repos, transactions)
 browser  ──►  web, api   ─┼──►   domain   ◄─────────┼─► events broker  (domain.EventPublisher)
                           │   (stdlib only)         └─► crypto/argon2  (domain.PasswordHasher)
                 cmd/idrometer wires it all together
```

| Package | Role |
|---|---|
| `internal/domain` | entities, rules, services (`Ingestor`, `Sweeper`, `Editor`, `Queries`, `Auth`) and the **ports** they need. Imports only the standard library. |
| `internal/store/sqlite` | implements the ports with SQLite (pure Go driver, no CGO) and embedded migrations. |
| `internal/store/memory` | in-memory implementation used by tests. |
| `internal/store/storetest` | conformance suite **every store must pass**, so the in-memory fake can't drift from SQLite. |
| `internal/api`, `internal/api/ingest` | device ingest (a decoder per hydrometer type) and the read-only JSON/CSV API. |
| `internal/web` | the HTML UI (templates, htmx, uPlot, all embedded in the binary). |
| `internal/events` | in-process pub/sub that feeds live graph updates over Server-Sent Events. |
| `internal/backup` | scheduled snapshots. |
| `cmd/idrometer` | the composition root. The only place that imports everything. |

The rules are enforced in CI by `depguard` (`.golangci.yml`), and by a test that the domain has no non-stdlib imports.

## Adding things

- **A new hydrometer** (Tilt, RAPT Pill, GravityMon…): add a package under `internal/api/ingest/<name>` implementing
  `ingest.Decoder`, which turns that device's request into a `domain.ReadingInput`, and register it in `cmd/idrometer`.
  The domain doesn't change.
- **Postgres**: implement the ports in `internal/store/postgres`, run `storetest.Run` against it, and choose it in
  `cmd/idrometer`. The schema uses plain SQL and a partial unique index that Postgres supports as well.
- **Alerts / notifications / forwarding to other services**: define a port in the domain, implement it in
  `internal/clients/<name>`, and inject it. (The lint rules already expect that directory.)

## Data model in one paragraph

A **device** is identified by `(source, hardware id)`. A **fermentation** belongs to one device and is `active` or
`completed`; a partial unique index guarantees at most one active, non-deleted fermentation per device. **Readings**
belong to a fermentation and keep the device's raw payload. Hand **measurements**, timeline **annotations**, a per-device
**calibration**, **recipes** and **settings** hang off those. Timestamps are stored as UTC milliseconds.
