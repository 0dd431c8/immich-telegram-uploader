package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/0dd431c8/immich-telegram-upload/immich"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type App struct {
	config config.Config
	immich immich.ImmichClient
}

func New(cfg config.Config) App {
	immich := immich.New(cfg, context.Background())
	return App{cfg, immich}
}

func (app *App) SetupBot(ctx context.Context, b *bot.Bot) {
	_, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "start", Description: "Start the bot"},
		},
	})

	if err != nil {
		panic(err)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeCommand, app.onStartHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeContains, app.onMessageHandler)

	b.Start(ctx)
}

func (app *App) onStartHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Starting for %s", update.Message.From.Username),
	})
}

func downloadFile(ctx context.Context, b *bot.Bot, immich *immich.ImmichClient, fileId string, filename string) error {
	fmt.Println("Getting file from telegram")

	file, err := b.GetFile(ctx, &bot.GetFileParams{
		FileID: fileId,
	})

	if err != nil {
		return err
	}

	downloadUrl := b.FileDownloadLink(file)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadUrl, nil)

	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return err
	}

	immich.UploadAsset(resp.Body, filename)

	return nil
}

func processDocument(ctx context.Context, b *bot.Bot, immich *immich.ImmichClient, chatId int64, document *models.Document) error {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return errors.New("unsupported filetype")
	}

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatId,
		Text:   "⏳ Uploading...",
	})

	if err != nil {
		fmt.Println(err)
	}

	err = downloadFile(ctx, b, immich, document.FileID, document.FileName)

	return err
}

func (app *App) onMessageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if update.Message.Document != nil {
		err := processDocument(ctx, b, &app.immich, update.Message.Chat.ID, update.Message.Document)

		if err != nil {
			fmt.Println(err)
		}

		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "✅ Uploaded successfully!",
		})

		if err != nil {
			fmt.Println(err)
		}

		return
	}

	if app.config.OnlyFileAttachment {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "⚠️ Please, send attachment as a file ⚠️",
		})

		if err != nil {
			fmt.Println(err)
		}
	}
}
