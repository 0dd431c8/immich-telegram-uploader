# Repository Guidelines

## Project Structure & Module Organization

This Go bot sends Telegram image/video documents to Immich. Hosted Bot API mode streams downloads over HTTP without local media storage. Local mode reads files downloaded by a local Telegram Bot API server; the included Compose deployment uses a shared volume for those files.

- `main.go`: optional `.env` loading, configuration validation, interrupt handling.
- `app/`: dependency composition and application startup.
- `config/`: environment-backed settings.
- `telegram/`: handlers, document filtering, HTTP downloads or local-file reads, upload progress, and enable/disable timers.
- `immich/`: authenticated requests, multipart uploads, asset metadata, and album operations.
- `telegram/*_test.go` and `immich/client_test.go`: integration tests using local HTTP servers; local-mode upload tests also use temporary files.
- `Dockerfile`, `compose.yaml`, `.dockerignore`: container build and deployment.

Keep changes within these package boundaries. Uploads pass through an `io.Reader` callback. Parse bot environment settings through `config.Config` and inject them into handlers rather than calling `os.Getenv` in upload code.

## Build, Test, and Development Commands

Use Go 1.27.2, as declared in `go.mod`.

- `go build ./...`: compile packages.
- `go run .`: run with environment variables and optional `.env`.
- `go test ./...`: run tests; use `go test -race ./...` for concurrency changes.
- `go vet ./...`: check common mistakes.
- `gofmt -w main.go app/*.go config/*.go telegram/*.go immich/*.go`: format sources.
- `go mod tidy`: reconcile dependency changes.
- `docker compose config --quiet`: validate deployment configuration; requires `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`, and an `.env` file for the bot service.
- `docker compose up -d --build`: build and deploy; `docker compose logs -f immich-telegram-uploader` shows logs.

## Coding Style & Naming Conventions

Follow `gofmt`: tabs, grouped imports, exported `PascalCase`, unexported `camelCase`, and lowercase package names. Pass contexts through network operations, close response bodies, and return routine integration errors. Synchronize shared handler state and make channel operations cancellation-aware.

## Testing Guidelines

Use standard `testing`, adjacent `*_test.go` files, and `TestBehavior` names. Prefer table-driven filtering cases, `httptest`, and in-memory readers. Cover HTTP failures, reader errors, response cleanup, cancellation, and progress state when modifying those paths. Avoid live Telegram/Immich calls. No coverage threshold is configured.

Keep hosted and local upload paths covered. Local-mode tests should use `t.TempDir()` and mocked `getFile` responses to cover successful reads, missing files, rejected API requests, cancellation, and upload errors. Check the `getFile` error before accessing its result, and close both HTTP bodies and opened files.

## Commit & Pull Request Guidelines

History uses short descriptions with mixed capitalization, such as `add docker` and `Add per-client upload albums`; no prefix convention is established. Keep commits focused. PRs should explain behavior, configuration changes, validation, and related issues.

## Security & Configuration Tips

Copy `.env.example` to ignored `.env`; never commit credentials. Required: `TELEGRAM_BOT_TOKEN`, numeric `TELEGRAM_BOT_ADMIN_ID`, `IMMICH_API_URL`, and `IMMICH_TOKEN`. Set `IMMICH_ALBUM_ID` or `IMMICH_PER_CLIENT_ALBUM=true`. Admin identity controls upload toggles, not uploader authorization. Regular photo/video messages are unimplemented. Non-document messages reaching the enabled upload handler always receive the attachment reminder. Keep command handlers and username checks ahead of that fallback.

Compose additionally requires `TELEGRAM_API_ID` and `TELEGRAM_API_HASH` for the API server. `.env.example` includes placeholders for those credentials and selects the included deployment with `TELEGRAM_BOT_API_URL=http://telegram-bot-api:8081` and `TELEGRAM_LOCAL_MODE=true`. The URL is a base address without `/bot` or the token. An empty URL uses the hosted API; local mode defaults to `false` when omitted and requires a custom URL when enabled. Keep the example configuration and README.md aligned with changes to `config.Config` and Compose.

The API server uses `TELEGRAM_LOCAL=1`; the Go bot uses the separate `TELEGRAM_LOCAL_MODE` setting. Preserve the shared `telegram-bot-api-data` volume at `/var/lib/telegram-bot-api` in both containers, the bot's read-only mount, and supplementary group `101`. Local mode needs readable files and traversable directories. The server stores downloaded media on disk, and the bot does not remove it after upload. Progress tracks transfer into Immich after the local download completes.

When moving from Telegram's hosted API, stop the old bot instance and call hosted `logOut` once before connecting locally. Hosted login is unavailable for 10 minutes afterward; local login can start immediately. Do not automate logout on every startup. Custom API URLs use a one-minute initialization timeout and a 30-minute HTTP client timeout. Compose `depends_on` orders startup but does not ensure the server has connected to Telegram; inspect API server logs when diagnosing `getMe` timeouts.

Include `/api` in the Immich URL; container `localhost` refers to the bot. Compose sends `SIGINT`, matching startup cancellation. Keep the Docker builder version aligned with `go.mod`.
