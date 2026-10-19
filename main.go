package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/0dd431c8/immich-telegram-upload/app"
	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	cfg, err := env.ParseAs[config.Config]()

	if err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	app := app.New(cfg, ctx, logger)

	app.Start()
}
