# Repository Guidelines

## Project Structure & Module Organization

This is a small Go Telegram bot that streams image and video document attachments from Telegram to Immich using multipart uploads. Media is not saved to local storage.

- `main.go` loads an optional `.env` file, parses configuration, creates the application, and handles interrupt cancellation.
- `app/app.go` wires the Immich client to the Telegram manager and starts the bot.
- `config/config.go` defines environment-backed configuration.
- `telegram/manager.go` contains bot setup, command and message handlers, media filtering, download streaming, and per-chat upload progress reporting.
- `immich/client.go` contains authenticated Immich HTTP requests, multipart asset uploads, album assignment, and an unfinished album-listing helper.
- `go.mod` and `go.sum` declare the Go toolchain and dependencies.
- There are currently no tests or checked-in assets.

Keep startup wiring in `main.go`, dependency composition in `app/`, Telegram behavior in `telegram/`, and Immich API operations in `immich/`. Uploads stream through an `io.Reader` callback from the Telegram manager to the Immich client.

## Build, Test, and Development Commands

Use the Go toolchain declared in `go.mod`.

- `go build ./...` — compile all packages.
- `go run .` — start the bot using environment variables and an optional local `.env` file; stop with Ctrl+C.
- `go test ./...` — run all tests once tests are added.
- `go vet ./...` — check for common Go mistakes.
- `gofmt -w main.go app/*.go config/*.go telegram/*.go immich/*.go` — format source files.
- `go mod tidy` — reconcile dependencies after changing imports.

## Coding Style & Naming Conventions

Follow standard Go formatting: tabs for indentation, grouped imports, and `gofmt` output. Use exported `PascalCase` names, unexported `camelCase` names, and short lowercase package names. Pass `context.Context` through network operations, check errors, and close response bodies and files. Keep media-specific processing in focused helpers.

Telegram handlers run concurrently. Synchronize shared mutable state and make channel operations cancellation-aware. Return integration errors to callers so handlers can report failures; avoid panicking on routine network errors.

## Testing Guidelines

Use Go's standard `testing` package, placing `*_test.go` files beside the code they cover. Name tests `TestBehavior` and use table-driven cases for attachment filtering and configuration defaults. Use `httptest` and in-memory readers for download and multipart upload tests to avoid live Telegram or Immich calls. Cover HTTP failures, response cleanup, cancellation, and upload progress state when changing those paths. Use `go test -race ./...` when adding tests for concurrent handlers or progress reporting. No coverage threshold or CI test configuration currently exists.

## Commit & Pull Request Guidelines

Existing commits use short, capitalized descriptions such as `Tidy` and `App package; handle document uploads`; no formal prefix convention is established. Keep commits focused. PRs should describe the behavior changed, relevant configuration changes, and validation performed. Link related issues when applicable.

## Security & Configuration Tips

Required environment variables:

- `TELEGRAM_BOT_TOKEN` — Telegram bot token.
- `TELEGRAM_BOT_ADMIN_USERNAME` — username checked against the message chat's username for `/enable` and `/disable`; it does not restrict who can upload media.
- `IMMICH_API_URL` — Immich API base URL used to construct asset and album endpoints.
- `IMMICH_TOKEN` — Immich API key sent in the `x-api-key` header.

Optional environment variables:

- `IMMICH_ALBUM_ID` — album to which uploaded assets are added.
- `ONLY_FILE_ATTACHMENT` — defaults to `true` and prompts users to send media as files. Setting it to `false` suppresses that prompt; regular photo and video message handling is not implemented yet.

Keep credentials in environment variables or the ignored `.env` file. Never commit tokens or downloaded media. If adding local media storage, add its directory to `.gitignore`.
