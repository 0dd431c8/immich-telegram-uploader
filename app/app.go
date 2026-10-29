package app

import (
	"context"
	"log/slog"

	"github.com/0dd431c8/immich-telegram-uploader/config"
	"github.com/0dd431c8/immich-telegram-uploader/immich"
	"github.com/0dd431c8/immich-telegram-uploader/telegram"
)

type App struct {
	config     config.Config
	immich     immich.Client
	botManager *telegram.Manager
}

func New(cfg config.Config, ctx context.Context, logger *slog.Logger) App {
	immichLogger := logger.With("component", "immich")
	telegramLogger := logger.With("component", "telegram")

	immich := immich.New(cfg, ctx, immichLogger)
	botManager := telegram.New(cfg, ctx, immich.UploadAsset, telegramLogger)

	return App{cfg, immich, botManager}
}

func (app *App) Start() {
	app.botManager.Run()
}
