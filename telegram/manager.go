package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type UploadFunc func(asset io.Reader, filename string) error

type Manager struct {
	telegramToken      string
	adminUsername      string
	uploadsDisabled    *atomic.Bool
	onlyFileAttachment bool
	bot                *bot.Bot
	ctx                context.Context
	upload             UploadFunc
	uploadEvents       chan uploadEvent
	logger             *slog.Logger
}

type uploadState byte

const (
	uploadStarted uploadState = iota
	uploadFinished
)

type uploadEvent struct {
	chatId int64
	state  uploadState
}

func New(cfg config.Config, ctx context.Context, onUpload UploadFunc, logger *slog.Logger) Manager {
	manager := Manager{
		telegramToken:      cfg.TelegramToken,
		adminUsername:      cfg.AdminUsername,
		uploadsDisabled:    &atomic.Bool{},
		onlyFileAttachment: cfg.OnlyFileAttachment,
		ctx:                ctx,
		upload:             onUpload,
		uploadEvents:       make(chan uploadEvent, 16),
		logger:             logger,
	}

	return manager
}

func (manager *Manager) Run() {
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

	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, manager.handleStart)
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "enable", bot.MatchTypeCommand, manager.uploadsDisabledHandler(false))
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "disable", bot.MatchTypeCommand, manager.uploadsDisabledHandler(true))
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeExact, manager.handleMessage)

	go manager.watchUploadState(ctx)

	manager.bot.Start(ctx)
}

func (manager *Manager) watchUploadState(ctx context.Context) {
	workers := make(map[int64]chan uploadEvent)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-manager.uploadEvents:
			events, exists := workers[msg.chatId]

			if !exists {
				events = make(chan uploadEvent, 16)
				workers[msg.chatId] = events

				go manager.watchChatUploads(ctx, msg.chatId, events)
			}

			select {
			case events <- msg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (manager *Manager) watchChatUploads(ctx context.Context, chatId int64, events <-chan uploadEvent) {
	type chatUploadProgress struct {
		activeUploads uint
		totalUploads  uint
		messageId     int
	}

	progress := &chatUploadProgress{}
	logger := manager.logger.With("chat_id", chatId)

	renderProgressBar := func(total uint, active uint) string {
		size := 10

		progress := float32(total-active) / float32(total)

		filled := int(float32(size) * progress)

		bar := strings.Repeat("█", filled)
		bar += strings.Repeat("░", size-filled)

		return fmt.Sprintf("[%s] %d/%d", bar, total-active, total)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-events:
			state := msg.state

			if state == uploadStarted {
				progress.activeUploads += 1
				progress.totalUploads += 1
			} else {
				progress.activeUploads -= 1
			}

			if progress.messageId == 0 {
				msg, err := manager.sendMessage(
					chatId,
					"Uploading...",
				)

				if err != nil {
					logger.Error("Failed to send a message", "error", err)
				}

				progress.messageId = msg.ID
			}

			if progress.activeUploads > 0 {
				_, err := manager.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    chatId,
					MessageID: int(progress.messageId),
					Text: fmt.Sprintf(
						"Uploading...\r\n%s",
						renderProgressBar(progress.totalUploads, progress.activeUploads),
					),
				})

				if err != nil {
					logger.Error("Failed to edit message", "message_id", progress.messageId, "error", err)
				}
			} else {
				_, err := manager.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
					ChatID:    chatId,
					MessageID: progress.messageId,
				})

				if err != nil {
					logger.Error("Failed to delete message", "message_id", progress.messageId, "error", err)
				}

				_, err = manager.sendMessage(
					chatId,
					fmt.Sprintf("✅ %d files uploaded successfully", progress.totalUploads),
				)

				if err != nil {
					logger.Error("Failed to send message", "error", err)
				}

				progress.totalUploads = 0
				progress.messageId = 0

			}
		}
	}
}

func (manager *Manager) uploadsDisabledHandler(disabled bool) func(ctx context.Context, b *bot.Bot, update *models.Update) {
	admin := manager.adminUsername

	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message.Chat.Username != admin {
			return
		}

		manager.logger.Info("Set uploads disabled", "disabled", disabled)

		manager.uploadsDisabled.Store(disabled)
	}
}

func (manager *Manager) handleStart(ctx context.Context, b *bot.Bot, update *models.Update) {
	attachmentModeText := ""

	if manager.onlyFileAttachment {
		attachmentModeText = "as a file attachment"
	}

	text := strings.Join([]string{
		"Send photos and videos",
		attachmentModeText,
		"to upload",
	}, " ")

	manager.logger.Info("New user", "telegram_handle", update.Message.Chat.Username)

	_, err := manager.sendMessage(
		update.Message.Chat.ID,
		text,
	)

	if err != nil {
		manager.logger.Error("Failed to send message", "error", err)
	}
}

func (manager *Manager) sendMessage(chatId int64, text string) (*models.Message, error) {
	message, err := manager.bot.SendMessage(manager.ctx, &bot.SendMessageParams{
		ChatID: chatId,
		Text:   text,
	})

	return message, err
}

func streamFileToUpload(ctx context.Context, b *bot.Bot, onUpload UploadFunc, fileId string, filename string) error {
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

	return onUpload(resp.Body, filename)
}

func (manager *Manager) processDocument(ctx context.Context, b *bot.Bot, onUpload UploadFunc, chatId int64, document *models.Document) error {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return errors.New("unsupported filetype")
	}

	select {
	case manager.uploadEvents <- uploadEvent{chatId, uploadStarted}:
	case <-ctx.Done():
		return ctx.Err()
	}

	err := streamFileToUpload(ctx, b, onUpload, document.FileID, document.FileName)
	select {
	case manager.uploadEvents <- uploadEvent{chatId, uploadFinished}:
	case <-ctx.Done():
		return ctx.Err()
	}

	return err
}

func (manager *Manager) handleMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if manager.uploadsDisabled.Load() {
		go func() {
			_, err := manager.sendMessage(
				update.Message.Chat.ID,
				"🛑 Uploads are disabled",
			)

			if err != nil {
				manager.logger.Error("Failed to send message", "error", err)
			}
		}()

		return
	}

	if update.Message.Document != nil {
		go func() {
			logger := manager.logger.With("telegram_handle", update.Message.Chat.Username)

			logger.Info("Processing document", "filename", update.Message.Document.FileName, "size", update.Message.Document.FileSize)

			err := manager.processDocument(ctx, b, manager.upload, update.Message.Chat.ID, update.Message.Document)

			if err != nil {
				logger.Error("Failed to process document", "error", err)

				return
			}

			logger.Info("Processing document finished", "telegram_handle", update.Message.Chat.Username)
		}()
		return
	}

	if manager.onlyFileAttachment {
		go func() {
			_, err := manager.sendMessage(
				update.Message.Chat.ID,
				"⚠️ Please, send attachment as a file ⚠️",
			)

			if err != nil {
				manager.logger.Error("Failed to send message", "error", err)
			}
		}()
	}
}
