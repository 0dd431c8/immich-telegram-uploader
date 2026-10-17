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

type OnUpload func(asset io.Reader, filename string) error

type Manager struct {
	telegramToken      string
	adminUsername      string
	uploadsDisabled    bool
	onlyFileAttachment bool
	bot                *bot.Bot
	ctx                context.Context
	onUpload           OnUpload
	uploadStarted      chan int64
	uploadFinished     chan int64
}

func New(cfg config.Config, ctx context.Context, onUpload OnUpload) Manager {
	manager := Manager{
		telegramToken:      cfg.TelegramToken,
		adminUsername:      cfg.AdminUsername,
		uploadsDisabled:    false,
		onlyFileAttachment: cfg.OnlyFileAttachment,
		ctx:                ctx,
		onUpload:           onUpload,
		uploadStarted:      make(chan int64),
		uploadFinished:     make(chan int64),
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

	go manager.watchUploadState(ctx)

	manager.bot.Start(ctx)
}

func (manager *Manager) watchUploadState(ctx context.Context) {
	type uploadState struct {
		activeUploads uint
		totalUploads  uint
		messageId     int
	}

	userUploads := make(map[int64]*uploadState)

	renderProgressBar := func(total uint, active uint) string {
		size := 10

		progress := float32(total-active) / float32(total)

		filled := int(float32(size) * progress)

		bar := strings.Repeat("█", filled)
		bar += strings.Repeat("░", size-filled)

		return fmt.Sprintf("[%s] %d/%d", bar, total-active, total)
	}

	for {
		var alteredChatId int64

		select {
		case <-ctx.Done():
			return
		case chatId := <-manager.uploadStarted:
			if userUploads[chatId] == nil {
				userUploads[chatId] = &uploadState{}
			}
			userUploads[chatId].activeUploads += 1
			userUploads[chatId].totalUploads += 1
			alteredChatId = chatId
		case chatId := <-manager.uploadFinished:
			if userUploads[chatId] == nil {
				userUploads[chatId] = &uploadState{}
			}
			userUploads[chatId].activeUploads -= 1
			alteredChatId = chatId
		}

		if alteredChatId != 0 {
			userUpload := userUploads[alteredChatId]

			if userUpload.messageId == 0 {
				msg, err := manager.bot.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: alteredChatId,
					Text:   "Uploading...",
				})

				if err != nil {
					fmt.Println(err)
				}

				userUpload.messageId = msg.ID
			}

			if userUpload.activeUploads > 0 {
				_, err := manager.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    alteredChatId,
					MessageID: int(userUpload.messageId),
					Text: fmt.Sprintf(
						"Uploading...\r\n%s",
						renderProgressBar(userUpload.totalUploads, userUpload.activeUploads),
					),
				})

				if err != nil {
					fmt.Println(err)
				}
			} else {
				_, err := manager.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
					ChatID:    alteredChatId,
					MessageID: userUpload.messageId,
				})

				userUpload.totalUploads = 0
				userUpload.messageId = 0

				if err != nil {
					fmt.Println(err)
				}
			}
		}
	}
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

func (manager *Manager) processDocument(ctx context.Context, b *bot.Bot, onUpload OnUpload, chatId int64, document *models.Document) error {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return errors.New("unsupported filetype")
	}

	manager.uploadStarted <- chatId

	// _, err := b.SendMessage(ctx, &bot.SendMessageParams{
	// 	ChatID: chatId,
	// 	Text:   "⏳ Uploading...",
	// })
	//
	// if err != nil {
	// 	fmt.Println(err)
	// }

	err := downloadFile(ctx, b, onUpload, document.FileID, document.FileName)

	manager.uploadFinished <- chatId

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
		err := manager.processDocument(ctx, b, manager.onUpload, update.Message.Chat.ID, update.Message.Document)

		if err != nil {
			fmt.Println(err)
		}

		// _, err = b.SendMessage(ctx, &bot.SendMessageParams{
		// 	ChatID: update.Message.Chat.ID,
		// 	Text:   "✅ Uploaded successfully!",
		// })
		//
		// if err != nil {
		// 	fmt.Println(err)
		// }

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
