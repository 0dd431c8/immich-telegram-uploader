package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
)

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
