package telegram

import (
	"context"
	"io"
	"log/slog"
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
	nextUploadID       atomic.Int64
	disableTimer       *time.Timer
	timerGeneration    uint64
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

func (manager *Manager) sendMessage(chatId int64, text string) (*models.Message, error) {
	message, err := manager.bot.SendMessage(manager.ctx, &bot.SendMessageParams{
		ChatID: chatId,
		Text:   text,
	})

	return message, err
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
