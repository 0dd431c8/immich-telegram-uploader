package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

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
	case manager.reporter.events <- uploadStarted{
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
	case manager.reporter.events <- uploadFinished{
		chatID:   chatID,
		uploadID: uploadID,
		err:      err,
	}:
	case <-ctx.Done():
		return ctx.Err()
	}

	return err
}
