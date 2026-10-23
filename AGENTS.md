# Repository Guidelines

## Project Structure & Module Organization

This Go bot streams Telegram image/video documents to Immich without storing media locally.

- `main.go`: optional `.env` loading, configuration validation, interrupt handling.
- `app/`: dependency composition and application startup.
- `config/`: environment-backed settings.
- `telegram/`: handlers, document filtering, download streaming, upload progress, and enable/disable timers.
- `immich/`: authenticated requests, multipart uploads, asset metadata, and album operations.
- `telegram/manager_test.go` and `immich/client_test.go`: integration tests using local HTTP servers.
- `Dockerfile`, `compose.yaml`, `.dockerignore`: container build and deployment.

Keep changes within these package boundaries. Uploads pass through an `io.Reader` callback.

## Build, Test, and Development Commands

Use Go 1.27.2, as declared in `go.mod`.

- `go build ./...`: compile packages.
- `go run .`: run with environment variables and optional `.env`.
- `go test ./...`: run tests; use `go test -race ./...` for concurrency changes.
- `go vet ./...`: check common mistakes.
- `gofmt -w main.go app/*.go config/*.go telegram/*.go immich/*.go`: format sources.
- `go mod tidy`: reconcile dependency changes.
- `docker compose config --quiet`: validate deployment configuration.
- `docker compose up -d --build`: build and deploy; `docker compose logs -f immich-telegram-upload` shows logs.

## Coding Style & Naming Conventions

Follow `gofmt`: tabs, grouped imports, exported `PascalCase`, unexported `camelCase`, and lowercase package names. Pass contexts through network operations, close response bodies, and return routine integration errors. Synchronize shared handler state and make channel operations cancellation-aware.

## Testing Guidelines

Use standard `testing`, adjacent `*_test.go` files, and `TestBehavior` names. Prefer table-driven filtering cases, `httptest`, and in-memory readers. Cover HTTP failures, reader errors, response cleanup, cancellation, and progress state when modifying those paths. Avoid live Telegram/Immich calls. No coverage threshold is configured.

## Commit & Pull Request Guidelines

History uses short descriptions with mixed capitalization, such as `add docker` and `Add per-client upload albums`; no prefix convention is established. Keep commits focused. PRs should explain behavior, configuration changes, validation, and related issues.

## Security & Configuration Tips

Copy `.env.example` to ignored `.env`; never commit credentials. Required: `TELEGRAM_BOT_TOKEN`, numeric `TELEGRAM_BOT_ADMIN_ID`, `IMMICH_API_URL`, and `IMMICH_TOKEN`. Set `IMMICH_ALBUM_ID` or `IMMICH_PER_CLIENT_ALBUM=true`. Admin identity controls upload toggles, not uploader authorization. Regular photo/video messages are unimplemented.

Include `/api` in the Immich URL; container `localhost` refers to the bot. Compose sends `SIGINT`, matching startup cancellation. Keep the Docker builder version aligned with `go.mod`.
