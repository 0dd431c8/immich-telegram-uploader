package app

import (
	"context"

	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/0dd431c8/immich-telegram-upload/immich"
	"github.com/0dd431c8/immich-telegram-upload/telegram"
)

type App struct {
	config     config.Config
	immich     immich.ImmichClient
	botManager telegram.Manager
}

func New(cfg config.Config, ctx context.Context) App {
	immich := immich.New(cfg, ctx)
	botManager := telegram.New(cfg, ctx, immich.UploadAsset)

	return App{cfg, immich, botManager}
}

func (app *App) Start() {
	app.botManager.SetupBot()
}
