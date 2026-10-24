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
	nextUploadID       atomic.Int64
	disableTimer       *time.Timer
	timerGeneration    uint64
}

type uploadEvent interface {
	isUploadEvent()
	ChatID() int64
}

type uploadStarted struct {
	chatID   int64
	uploadID int64
	username string
	fileSize int64
	sent     *atomic.Int64
}

func (uploadStarted) isUploadEvent() {}
func (e uploadStarted) ChatID() int64 {
	return e.chatID
}

type uploadFinished struct {
	chatID   int64
	uploadID int64
	err      error
}

func (uploadFinished) isUploadEvent() {}
func (e uploadFinished) ChatID() int64 {
	return e.chatID
}

type uploadProgress struct {
	chatID   int64
	uploadID int64
	sent     int64
	total    int64
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
				workers[started.chatID] = events

				go manager.watchChatUploads(ctx, started.chatID, started.username, events)
			}

			select {
			case events <- msg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (manager *Manager) watchChatUploads(ctx context.Context, chatID int64, username string, events <-chan uploadEvent) {
	type upload struct {
		total    int64
		sent     *atomic.Int64
		finished bool
		err      error
	}

	type chatUploadProgress struct {
		perFileProgress map[int64]upload
		messageID       int
		lastText        string
	}

	progress := &chatUploadProgress{
		perFileProgress: make(map[int64]upload),
	}

	logger := manager.logger.With("username", username)

	renderProgressBar := func() string {
		size := 10

		var sent, total int64

		formatBytes := func(bytes int64) float32 {
			mb := float32(bytes) / 1024 / 1024

			return mb
		}

		for _, p := range progress.perFileProgress {
			sent += p.sent.Load()
			total += p.total
		}

		progress := float32(0)

		if total > 0 {
			progress = float32(sent) / float32(total)
		}

		filled := int(float32(size) * progress)
		filled = max(0, min(size, filled))

		bar := strings.Repeat("█", filled)
		bar += strings.Repeat("░", size-filled)

		return fmt.Sprintf("[%s] %.2f/%.2fMB", bar, formatBytes(sent), formatBytes(total))
	}

	updateEvery := 100 * time.Millisecond

	ticker := time.NewTicker(updateEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-events:
			switch e := msg.(type) {
			case uploadStarted:
				progress.perFileProgress[e.uploadID] = upload{total: e.fileSize, sent: e.sent}
			case uploadFinished:
				p, exists := progress.perFileProgress[e.uploadID]

				if !exists {
					continue
				}

				p.finished = true
				p.err = e.err

				progress.perFileProgress[e.uploadID] = p
			}
		case <-ticker.C:
			if len(progress.perFileProgress) == 0 {
				continue
			}

			allFinished := true
			succeeded, failed := 0, 0

			for _, p := range progress.perFileProgress {
				if !p.finished {
					allFinished = false
					continue
				}

				if p.err != nil {
					failed += 1
				} else {
					succeeded += 1
				}
			}

			text := fmt.Sprintf("Uploading...\r\n%s", renderProgressBar())

			if allFinished {
				text = fmt.Sprintf(
					"✅ %d files uploaded successfully",
					succeeded,
				)

				if failed > 0 {
					text += fmt.Sprintf("\n❌ %d files failed", failed)
				}
			}

			if progress.messageID == 0 {
				msg, err := manager.sendMessage(chatID, text)

				if err != nil {
					logger.Error("Failed to send progress message", "error", err)
					continue
				}

				progress.messageID = msg.ID
				progress.lastText = text
			} else if text != progress.lastText {
				_, err := manager.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    chatID,
					MessageID: int(progress.messageID),
					Text:      text,
				})

				if err != nil {
					logger.Error("Failed to edit message", "message_id", progress.messageID, "error", err)
					continue
				}

				progress.lastText = text
			}

			if !allFinished {
				continue
			}

			if chatID != manager.adminUserID {
				_, err := manager.sendMessage(
					manager.adminUserID,
					fmt.Sprintf(
						"Uploads from %s: %d succeeded, %d failed",
						username, succeeded, failed,
					),
				)

				if err != nil {
					logger.Error("Failed to notify admin", "error", err)
				}
			}

			progress.perFileProgress = make(map[int64]upload)
			progress.messageID = 0
			progress.lastText = ""
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

func streamFileToUpload(
	ctx context.Context,
	b *bot.Bot,
	fileId string,
	filename string,
	fileSize int64,
	author string,
	onUpload UploadFunc,
	onProgress ProgressFunc,
) error {
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

	progressReader := &ProgressReader{
		Reader:     resp.Body,
		Total:      fileSize,
		OnProgress: onProgress,
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return errors.New("failed to download file " + filename)
	}

	return onUpload(progressReader, filename, author)
}

func (manager *Manager) processDocument(ctx context.Context, b *bot.Bot, onUpload UploadFunc, author string, chatID int64, document *models.Document) error {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return errors.New("unsupported filetype")
	}

	uploadID := manager.nextUploadID.Add(1)
	sentBytes := &atomic.Int64{}

	select {
	case manager.uploadEvents <- uploadStarted{
		chatID:   chatID,
		uploadID: uploadID,
		username: author,
		fileSize: document.FileSize,
		sent:     sentBytes,
	}:
	case <-ctx.Done():
		return ctx.Err()
	}

	onProgress := func(sent, total int64) {
		sentBytes.Store(sent)
	}

	err := streamFileToUpload(
		ctx,
		b,
		document.FileID,
		document.FileName,
		document.FileSize,
		author,
		onUpload,
		onProgress,
	)

	select {
	case manager.uploadEvents <- uploadFinished{
		chatID:   chatID,
		uploadID: uploadID,
		err:      err,
	}:
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
