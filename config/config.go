package config

import (
	"fmt"
	"net/url"
)

type Config struct {
	TelegramToken        string `env:"TELEGRAM_BOT_TOKEN,required"`
	TelegramAdminUserID  int64  `env:"TELEGRAM_BOT_ADMIN_ID,required"`
	TelegramBotAPIURL    string `env:"TELEGRAM_BOT_API_URL"`
	TelegramLocalMode    bool   `env:"TELEGRAM_LOCAL_MODE" envDefault:"false"`
	ImmichAPIURL         string `env:"IMMICH_API_URL,required"`
	ImmichToken          string `env:"IMMICH_TOKEN,required"`
	ImmichPerClientAlbum bool   `env:"IMMICH_PER_CLIENT_ALBUM" envDefault:"false"`
	ImmichAlbumId        string `env:"IMMICH_ALBUM_ID"`
}

func (c *Config) Validate() error {
	if c.TelegramLocalMode && c.TelegramBotAPIURL == "" {
		return fmt.Errorf(
			"TELEGRAM_BOT_API_URL must be set when TELEGRAM_LOCAL_MODE is enabled",
		)
	}

	if c.ImmichAlbumId == "" && !c.ImmichPerClientAlbum {
		return fmt.Errorf("IMMICH_ALBUM_ID or IMMICH_PER_CLIENT_ALBUM must be set")
	}

	_, err := url.Parse(c.ImmichAPIURL)

	if err != nil {
		return fmt.Errorf("invalid IMMICH_API_URL: %s", err.Error())
	}

	return nil
}
