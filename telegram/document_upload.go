package telegram

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type documentUploader struct {
	upload    UploadFunc
	reporter  *uploadReporter
	localMode bool
}

func newDocumentUploader(upload UploadFunc, reporter *uploadReporter, localMode bool) *documentUploader {
	return &documentUploader{
		upload,
		reporter,
		localMode,
	}
}

func streamFileToUpload(
	ctx context.Context,
	b *bot.Bot,
	fileId string,
	filename string,
	fileSize int64,
	author string,
	localMode bool,
	onUpload UploadFunc,
	onProgress ProgressFunc,
) error {
	file, err := b.GetFile(ctx, &bot.GetFileParams{
		FileID: fileId,
	})

	if err != nil {
		return err
	}

	if localMode {
		if err := ctx.Err(); err != nil {
			return err
		}

		localFile, err := os.Open(file.FilePath)

		if err != nil {
			return err
		}

		defer func() { _ = localFile.Close() }()

		progressReader := &ProgressReader{
			Reader:     localFile,
			Total:      fileSize,
			OnProgress: onProgress,
		}

		return onUpload(progressReader, filename, author)
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

func (uploader *documentUploader) processDocument(
	ctx context.Context,
	b *bot.Bot,
	author string,
	chatID int64,
	document *models.Document,
) error {
	if mime := document.MimeType; !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		return errors.New("unsupported filetype")
	}

	tracking, err := uploader.reporter.begin(
		ctx,
		chatID,
		author,
		document.FileSize,
	)

	if err != nil {
		return err
	}

	uploadErr := streamFileToUpload(
		ctx,
		b,
		document.FileID,
		document.FileName,
		document.FileSize,
		author,
		uploader.localMode,
		uploader.upload,
		tracking.progress,
	)

	if err := tracking.finish(ctx, uploadErr); err != nil {
		return err
	}

	return uploadErr
}
