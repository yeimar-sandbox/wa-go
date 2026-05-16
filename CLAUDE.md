# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Stack

Goravel (Laravel-like Go framework) on top of Gin, with a vendored fork of `whatsmeow` for the WhatsApp protocol. PostgreSQL is the primary DB; SQLite is supported for app data but whatsmeow's session store always uses Postgres (see Gotchas).

## Common commands

```bash
# Run the server (reads .env from cwd)
go run .

# Goravel CLI (migrations, code generation, etc.)
./artisan migrate                      # macOS/Linux
./artisan.bat migrate                  # Windows cmd
./artisan.ps1 migrate                  # Windows PowerShell

# Tests
go test ./tests/unit/... -count=1                    # unit tests, no DB needed
go test ./tests/feature/... -count=1                 # feature tests, boots full app
go test -race -run TestHealth ./tests/feature/...    # single test/suite

# Static checks
go vet ./app/... ./bootstrap/... ./config/... ./routes/...
golangci-lint run ./app/... ./bootstrap/... ./config/... ./routes/...

# Smoke test the API via Postman collection (server must be running)
newman run docs/wa-go-api.postman_collection.json --folder Health \
  --env-var baseUrl=http://localhost:3000/api/v1
```

## Architecture

### Boot flow
`main.go` → `bootstrap.Boot()` → `foundation.Setup()` wires migrations (`bootstrap/migrations.go`), routes (`routes/`), service providers (`bootstrap/providers.go`), and config. Then `bootstrap.ValidateEnv()` fails-fast if `APP_KEY`, `WA_GLOBAL_API_KEY`, or DB creds are missing (skips DB checks when `DB_CONNECTION=sqlite`; allows empty `APP_KEY` only when `APP_ENV=local`).

### DI container
`WhatsappServiceProvider` (`app/providers/whatsapp_service_provider.go`) registers two singletons:
- `whatsapp.manager` — `*whatsapp.Manager` (multi-instance client pool, owns the whatsmeow session container)
- `whatsapp.instance_service` — `*services.InstanceService`

Both are resolved in `routes/api.go` via `facades.App().MakeWith(...)`. The provider's `Boot()` also restores webhook registrations from DB and recovers from any panic so a missing whatsmeow store doesn't crash startup.

### Request path
```
HTTP → middleware (AdminAuth | InstanceAuth | Idempotency)
     → Controller (app/http/controllers/*)
     → Service (app/services/*)
     → Manager (app/whatsapp/manager.go) → whatsmeow client
     → DB (via Goravel ORM)
```
Controllers are thin: bind/validate request, call service, wrap response with `response.NewSuccess|NewCreated|Error`. Domain errors come from `app/errors` (typed `AppError`).

### Auth model
Two-tier, both via the `apikey` header (also `?apikey=` query for WebSocket):
- **Admin** routes (`/api/v1/instances` collection) — `WA_GLOBAL_API_KEY`.
- **Instance** routes (`/api/v1/instances/{id}/*`) — per-instance token returned at instance creation.

Middlewares: `middleware.AdminAuth()`, `middleware.InstanceAuth()`. Inside instance handlers, retrieve the resolved instance with `middleware.GetInstance(ctx)`.

### Event dispatch
`app/whatsapp/dispatcher.go` (`EventDispatcher`) fans whatsmeow events out to two transports:
- **Webhooks**: POST JSON, signed `X-Webhook-Signature: sha256=<hex>` using each target's secret. Wildcard subscriptions (`message.*`, `*`) are supported.
- **WebSocket**: buffered channels per subscriber; non-blocking sends drop events if a slow client backs up.

The dispatcher holds an `sync.RWMutex`: write-lock on register/unregister/subscribe, read-lock through the entire `Dispatch()` so `close(c)` from `UnsubscribeWs` cannot race a concurrent send. Hold this invariant when editing the file.

### Idempotency
`app/http/middleware/idempotency.go` keeps an in-memory `map[key]entry` keyed by the `Idempotency-Key` header. Capped at `IDEMPOTENCY_MAX_ENTRIES` (default 10000) with oldest-eviction on overflow plus a 24h time-based sweep. Replace with Redis if you ever need to share state across replicas.

### Routes
All routes live in `routes/api.go`. URL params: `{id}` = instance, `{msgId}` = message, `{groupId}`, `{chatId}`, `{jid}`, `{newsletterId}`, `{callId}`, `{webhookId}`, `{labelId}`. The README has the full endpoint table; `docs/wa-go-api.postman_collection.json` is the canonical request reference.

### Tests
- `tests/unit/` — pure Go, no DB, runs in CI.
- `tests/feature/` — uses `tests.TestCase` (extends `goravel/framework/testing.TestCase`) which calls `bootstrap.Boot()` in `init()` and creates tables via raw SQL helpers (`EnsureDB`, `ClearDB`, `CreateTestInstance`). Suites use `stretchr/testify/suite`.

## Gotchas

- **whatsmeow always wants Postgres.** `WhatsappServiceProvider.Register` opens `database/connections.postgres` (or `WA_AUTH_DATABASE_URL`) for the whatsmeow session store regardless of `DB_CONNECTION`. The provider's Boot has a `defer recover()` so a failure here logs a warning instead of crashing — but instance creation/connection will fail without Postgres. CI's smoke job runs a `postgres:16-alpine` service to satisfy this.
- **`whatsmeow-lib/` is a vendored fork** — do not touch it for app-level changes. The module imports `go.mau.fi/whatsmeow` via `replace` in `go.mod`.
- **Config files use `init()`** to call `config.Add()`. Each file in `config/` registers its slice of the config tree on package load; `config.Boot()` is intentionally empty.
- **`WA_CONNECT_ON_STARTUP=true`** auto-connects every persisted instance at boot. Disable for tests and local development to avoid surprise QR prompts.
- **CRLF on Windows.** Git is configured to normalize line endings; warnings during `git add` are expected and benign.

## CI

`.github/workflows/ci.yml` runs four jobs: `vet` (blocking), `lint` (advisory — golangci-lint v1.64, flip `continue-on-error` once clean), `test` (unit tests with `-race`), and `smoke` (boots the server against a Postgres service and runs `newman --folder Health`). Other Newman folders need a paired WhatsApp instance, so they're local-only.
