# Contributing

```sh
go test -race ./...        # all tests
golangci-lint run          # includes the architecture (import) rules
go run ./cmd/idrometer serve   # needs IDROMETER_INGEST_TOKEN (16+ chars)
go run ./cmd/simulate -token "$IDROMETER_INGEST_TOKEN"   # fake iSpindel
```

`task check` runs vet, tests, lint and the vulnerability scan; `task` lists the rest (needs [Task](https://taskfile.dev)).

Read `docs/architecture.md` first. In short: the domain imports only the standard library; adapters depend on it, never
the other way round. A new store must pass `internal/store/storetest`. Add a test with every change.
