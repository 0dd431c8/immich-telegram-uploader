package config

import (
	"fmt"
	"net/url"
)

type Config struct {
	TelegramToken        string `env:"TELEGRAM_BOT_TOKEN,required"`
	AdminUsername        string `env:"TELEGRAM_BOT_ADMIN_USERNAME,required"`
	ImmichAPIURL         string `env:"IMMICH_API_URL,required"`
	ImmichToken          string `env:"IMMICH_TOKEN,required"`
	ImmichPerClientAlbum bool   `env:"IMMICH_PER_CLIENT_ALBUM" envDefault:"false"`
	ImmichAlbumId        string `env:"IMMICH_ALBUM_ID"`
	OnlyFileAttachment   bool   `env:"ONLY_FILE_ATTACHMENT" envDefault:"true"`
}

func (c *Config) Validate() error {
	if c.ImmichAlbumId == "" && !c.ImmichPerClientAlbum {
		return fmt.Errorf("IMMICH_ALBUM_ID or IMMICH_PER_CLIENT_ALBUM must be set")
	}

	_, err := url.Parse(c.ImmichAPIURL)

	if err != nil {
		return fmt.Errorf("invalid IMMICH_API_URL: %s", err.Error())
	}

	return nil
}
