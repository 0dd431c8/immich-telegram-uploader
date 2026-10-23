package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type UploadFunc func(asset io.Reader, filename string, author string) error

type Manager struct {
	telegramToken      string
	adminUserID        int64
	uploadsDisabled    *atomic.Bool
	onlyFileAttachment bool
	bot                *bot.Bot
	ctx                context.Context
	upload             UploadFunc
	uploadEvents       chan uploadEvent
	logger             *slog.Logger

	uploadControlMutex sync.Mutex
	disableTimer       *time.Timer
	timerGeneration    uint64
}

type uploadEvent interface {
	isUploadEvent()
	ChatID() int64
}

type uploadStarted struct {
	id       int64
	username string
	fileSize int64
}

func (uploadStarted) isUploadEvent() {}
func (e uploadStarted) ChatID() int64 {
	return e.id
}

type uploadFinished struct {
	id int64
}

func (uploadFinished) isUploadEvent() {}
func (e uploadFinished) ChatID() int64 {
	return e.id
}

func New(cfg config.Config, ctx context.Context, onUpload UploadFunc, logger *slog.Logger) *Manager {
	manager := Manager{
		telegramToken:      cfg.TelegramToken,
		adminUserID:        cfg.TelegramAdminUserID,
		uploadsDisabled:    &atomic.Bool{},
		onlyFileAttachment: cfg.OnlyFileAttachment,
		ctx:                ctx,
		upload:             onUpload,
		uploadEvents:       make(chan uploadEvent, 16),
		logger:             logger,
	}

	return &manager
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
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "enable", bot.MatchTypeCommandStartOnly, manager.uploadsDisabledHandler(false))
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "disable", bot.MatchTypeCommand, manager.uploadsDisabledHandler(true))
	manager.bot.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeExact, manager.handleMessage)

	go manager.watchUploadState(ctx)

	manager.bot.Start(ctx)
}

func (manager *Manager) setUploadsEnabled(enabled bool, duration time.Duration) {
	manager.uploadControlMutex.Lock()
	defer manager.uploadControlMutex.Unlock()

	manager.timerGeneration++

	generation := manager.timerGeneration

	if manager.disableTimer != nil {
		manager.disableTimer.Stop()
		manager.disableTimer = nil
	}

	manager.uploadsDisabled.Store(!enabled)

	if !enabled || duration <= 0 {
		return
	}

	manager.disableTimer = time.AfterFunc(duration, func() {
		manager.uploadControlMutex.Lock()
		defer manager.uploadControlMutex.Unlock()

		if generation != manager.timerGeneration {
			return
		}

		manager.disableTimer = nil
		manager.uploadsDisabled.Store(true)
		manager.logger.Info("Disabled on timer")
	})
}

func (manager *Manager) watchUploadState(ctx context.Context) {
	workers := make(map[int64]chan uploadEvent)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-manager.uploadEvents:
			events, exists := workers[msg.ChatID()]

			if !exists {
				started, ok := msg.(uploadStarted)

				if !ok {
					continue
				}

				events = make(chan uploadEvent, 16)
				workers[started.id] = events

				go manager.watchChatUploads(ctx, started.id, started.username, events)
			}

			select {
			case events <- msg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (manager *Manager) watchChatUploads(ctx context.Context, chatId int64, username string, events <-chan uploadEvent) {
	type chatUploadProgress struct {
		activeUploads uint
		totalUploads  uint
		messageId     int
	}

	progress := &chatUploadProgress{}
	logger := manager.logger.With("username", username)

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
			switch msg.(type) {
			case uploadStarted:
				progress.activeUploads += 1
				progress.totalUploads += 1
			case uploadFinished:
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

				if chatId != manager.adminUserID {
					_, err = manager.sendMessage(
						manager.adminUserID,
						fmt.Sprintf("%d uploads from %s", progress.totalUploads, username),
					)
					if err != nil {
						logger.Error("Failed to notify admin", "admin_user_id", manager.adminUserID, "error", err)
					}
				}

				progress.totalUploads = 0
				progress.messageId = 0

			}
		}
	}
}

func (manager *Manager) uploadsDisabledHandler(disabled bool) func(ctx context.Context, b *bot.Bot, update *models.Update) {
	admin := manager.adminUserID

	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message.Chat.ID != admin {
			return
		}

		manager.logger.Info("Set uploads disabled", "disabled", disabled)

		if disabled {
			manager.setUploadsEnabled(false, 0)
		}

		if !disabled {
			msg := strings.Fields(update.Message.Text)
			var duration time.Duration = 0

			if len(msg) > 1 {
				d, err := strconv.ParseUint(msg[1], 10, 32)

				if err != nil {
					_, err := manager.sendMessage(update.Message.Chat.ID, "Invalid duration")

					if err != nil {
						manager.logger.Error("Failed to send message", "error", err)
					}

					return
				}

				duration = time.Second * time.Duration(d)
			}

			manager.setUploadsEnabled(true, duration)

			manager.logger.Info("Enabled for duration", "duration", duration/time.Second)
		}
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

func streamFileToUpload(ctx context.Context, b *bot.Bot, onUpload UploadFunc, fileId string, filename string, author string) error {
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

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return errors.New("failed to download file " + filename)
	}

	return onUpload(resp.Body, filename, author)
}

func (manager *Manager) processDocument(ctx context.Context, b *bot.Bot, onUpload UploadFunc, author string, chatId int64, document *models.Document) error {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return errors.New("unsupported filetype")
	}

	select {
	case manager.uploadEvents <- uploadStarted{chatId, author, 0}:
	case <-ctx.Done():
		return ctx.Err()
	}

	err := streamFileToUpload(ctx, b, onUpload, document.FileID, document.FileName, author)
	select {
	case manager.uploadEvents <- uploadFinished{chatId}:
	case <-ctx.Done():
		return ctx.Err()
	}

	return err
}

func (manager *Manager) handleMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if update.Message.Chat.Username == "" {
		manager.sendMessage(update.Message.Chat.ID, "🛑 You need a username to use this bot")
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

			err := manager.processDocument(ctx, b, manager.upload, update.Message.Chat.Username, update.Message.Chat.ID, update.Message.Document)

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
