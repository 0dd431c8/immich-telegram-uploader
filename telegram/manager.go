package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type OnUpload func(asset io.Reader, filename string)

type Manager struct {
	telegramToken      string
	adminUsername      string
	uploadsDisabled    bool
	onlyFileAttachment bool
	bot                *bot.Bot
	ctx                context.Context
	onUpload           OnUpload
}

func New(cfg config.Config, ctx context.Context, onUpload OnUpload) Manager {
	manager := Manager{
		telegramToken:      cfg.TelegramToken,
		adminUsername:      cfg.AdminUsername,
		uploadsDisabled:    false,
		onlyFileAttachment: cfg.OnlyFileAttachment,
		ctx:                ctx,
		onUpload:           onUpload,
	}

	return manager
}

func (manager *Manager) SetupBot() {
	ctx := manager.ctx
	b, err := bot.New(manager.telegramToken)

	if err != nil {
		panic(err)
	}

	defer func() { _, _ = b.Close(ctx) }()

	manager.bot = b

	_, err = manager.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "start", Description: "Start the bot"},
		},
	})

	if err != nil {
		panic(err)
	}

	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, manager.onStartHandler)
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "enable", bot.MatchTypeCommand, manager.onSetDisabled(false))
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "disable", bot.MatchTypeCommand, manager.onSetDisabled(true))
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeExact, manager.onMessageHandler)

	manager.bot.Start(ctx)
}

func (manager *Manager) onSetDisabled(disabled bool) func(ctx context.Context, b *bot.Bot, update *models.Update) {
	admin := manager.adminUsername

	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message.Chat.Username != admin {
			return
		}

		manager.uploadsDisabled = disabled
	}
}

func (manager *Manager) onStartHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	attachmentModeText := ""

	if manager.onlyFileAttachment {
		attachmentModeText = "as a file attachment"
	}

	text := strings.Join([]string{
		"Send photos and videos",
		attachmentModeText,
		"to upload",
	}, " ")

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   text,
	})

	fmt.Println(err)
}

func downloadFile(ctx context.Context, b *bot.Bot, onUpload OnUpload, fileId string, filename string) error {
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

	onUpload(resp.Body, filename)

	return nil
}

func processDocument(ctx context.Context, b *bot.Bot, onUpload OnUpload, chatId int64, document *models.Document) error {
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

	err = downloadFile(ctx, b, onUpload, document.FileID, document.FileName)

	return err
}

func (manager *Manager) onMessageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if manager.uploadsDisabled {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "🛑 Uploads are disabled",
		})

		if err != nil {
			fmt.Println(err)
		}

		return
	}

	if update.Message.Document != nil {
		err := processDocument(ctx, b, manager.onUpload, update.Message.Chat.ID, update.Message.Document)

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

	if manager.onlyFileAttachment {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "⚠️ Please, send attachment as a file ⚠️",
		})

		if err != nil {
			fmt.Println(err)
		}
	}
}
