package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/0dd431c8/immich-telegram-upload/app"
	"github.com/0dd431c8/immich-telegram-upload/immich"
	"github.com/caarlos0/env/v11"
	"github.com/go-telegram/bot"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	cfg, err := env.ParseAs[app.Config]()

	if err != nil {
		panic(err)
	}

	app := app.New(cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	b, err := bot.New(cfg.TelegramToken)

	immich := immich.New(cfg, ctx)

	immich.GetAllAlbums()

	if err != nil {
		panic(err)
	}

	defer b.Close(ctx)

	app.SetupBot(ctx, b)
	b.Start(ctx)
}
