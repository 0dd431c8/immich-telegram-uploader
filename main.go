package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"

	"github.com/caarlos0/env/v11"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
)

type Config struct {
	TelegramToken string `env:"TELEGRAM_BOT_TOKEN,required"`
}

func main() {
	_ = godotenv.Load()

	cfg, err := env.ParseAs[Config]()

	if err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
	}

	b, err := bot.New(cfg.TelegramToken, opts...)

	if err != nil {
		panic(err)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, onStart)

	b.Start(ctx)
}

func onStart(ctx context.Context, b *bot.Bot, update *models.Update) {
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Starting for %s", update.Message.From.Username),
	})
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Photo == nil {
		return
	}

	photos := update.Message.Photo
	photo := photos[len(photos)-1]

	file, err := b.GetFile(ctx, &bot.GetFileParams{
		FileID: photo.FileID,
	})

	if err != nil {
		return
	}

	downloadUrl := b.FileDownloadLink(file)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadUrl, nil)

	if err != nil {
		return
	}

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	out, err := os.Create(photo.FileUniqueID + ".jpg")

	if err != nil {
		return
	}

	defer out.Close()

	_, err = io.Copy(out, resp.Body)

	if err != nil {
		return
	}
}
