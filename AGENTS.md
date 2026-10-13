# Repository Guidelines

## Project Structure & Module Organization

This is a small Go Telegram bot that downloads image and video attachments to local storage.

- `main.go` loads configuration, creates the bot, and handles interrupt cancellation.
- `app/app.go` contains bot registration, message handlers, media filtering, and download helpers.
- `app/config.go` defines environment-backed configuration.
- `go.mod` and `go.sum` declare the Go toolchain and dependencies.
- `uploads/` is created at runtime for downloaded media. There are currently no tests or checked-in assets.

Keep application behavior in `app/` and startup wiring in `main.go`. Immich configuration exists, but uploading to Immich is not implemented yet.

## Build, Test, and Development Commands

Use the Go toolchain declared in `go.mod`.

- `go build ./...` — compile all packages.
- `go run .` — start the bot using environment variables and an optional local `.env` file; stop with Ctrl+C.
- `go test ./...` — run all tests once tests are added.
- `go vet ./...` — check for common Go mistakes.
- `gofmt -w main.go app/*.go` — format source files.
- `go mod tidy` — reconcile dependencies after changing imports.

## Coding Style & Naming Conventions

Follow standard Go formatting: tabs for indentation, grouped imports, and `gofmt` output. Use exported `PascalCase` names, unexported `camelCase` names, and short lowercase package names. Pass `context.Context` through network operations, check errors, and close response bodies and files. Keep media-specific processing in focused helpers.

## Testing Guidelines

Use Go's standard `testing` package, placing `*_test.go` files beside the code they cover. Name tests `TestBehavior` and use table-driven cases for attachment filtering and configuration defaults. Use `httptest` and temporary directories for download tests to avoid live Telegram calls. No coverage threshold or CI test configuration currently exists.

## Commit & Pull Request Guidelines

Existing commits use short, capitalized descriptions such as `Tidy` and `App package; handle document uploads`; no formal prefix convention is established. Keep commits focused. PRs should describe the behavior changed, relevant configuration changes, and validation performed. Link related issues when applicable.

## Security & Configuration Tips

Set required `TELEGRAM_BOT_TOKEN` and `IMMICH_API_URL`. `ONLY_FILE_ATTACHMENT` defaults to `true`; set it to `false` to also process regular photo and video messages. Keep credentials in environment variables or the ignored `.env` file. Never commit tokens or downloaded media; `uploads/` is not currently ignored.
