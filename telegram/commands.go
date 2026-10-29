package telegram

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (manager *Manager) uploadsDisabledHandler(disabled bool) func(ctx context.Context, b *bot.Bot, update *models.Update) {
	admin := manager.adminUserID

	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message.Chat.ID != admin {
			return
		}

		manager.logger.Info("Set uploads disabled", "disabled", disabled)

		if disabled {
			manager.control.setEnabled(false, 0)
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

			manager.control.setEnabled(true, duration)

			manager.logger.Info("Enabled for duration", "duration", duration/time.Second)
		}
	}
}

func (manager *Manager) handleStart(ctx context.Context, b *bot.Bot, update *models.Update) {
	text := "Send photos and videos as a file attachment to upload"

	manager.logger.Info("New user", "telegram_handle", update.Message.Chat.Username)

	_, err := manager.sendMessage(
		update.Message.Chat.ID,
		text,
	)

	if err != nil {
		manager.logger.Error("Failed to send message", "error", err)
	}
}
