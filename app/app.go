package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type App struct {
	config Config
}

func New(cfg Config) App {
	return App{cfg}
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

func downloadFile(ctx context.Context, b *bot.Bot, fileId string, filename string) {
	file, err := b.GetFile(ctx, &bot.GetFileParams{
		FileID: fileId,
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

	const uploadsDir = "uploads"

	_ = os.MkdirAll(uploadsDir, 0755)

	out, err := os.Create(path.Join(uploadsDir, filename))

	if err != nil {
		return
	}

	defer out.Close()

	_, err = io.Copy(out, resp.Body)

	if err != nil {
		panic(err)
	}
}

func processPhoto(ctx context.Context, b *bot.Bot, photos []models.PhotoSize) {
	if len(photos) < 1 {
		return
	}

	photo := photos[len(photos)-1]

	downloadFile(ctx, b, photo.FileID, fmt.Sprintf("%s.jpg", photo.FileUniqueID))
}

func processDocument(ctx context.Context, b *bot.Bot, document *models.Document) {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return
	}

	downloadFile(ctx, b, document.FileID, document.FileName)
}

func processVideo(ctx context.Context, b *bot.Bot, video *models.Video) {
	downloadFile(ctx, b, video.FileID, fmt.Sprintf("%s.mp4", video.FileUniqueID))
}

func (app *App) onMessageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if update.Message.Document != nil {
		processDocument(ctx, b, update.Message.Document)
	}

	if app.config.OnlyFileAttachment {
		return
	}

	if update.Message.Photo != nil {
		processPhoto(ctx, b, update.Message.Photo)
	}

	if update.Message.Video != nil {
		processVideo(ctx, b, update.Message.Video)
	}
}
