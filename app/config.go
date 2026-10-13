package app

type Config struct {
	TelegramToken      string `env:"TELEGRAM_BOT_TOKEN,required"`
	ImmichAPIURL       string `env:"IMMICH_API_URL,required"`
	ImmichToken        string `env:"IMMICH_TOKEN,required"`
	OnlyFileAttachment bool   `env:"ONLY_FILE_ATTACHMENT" envDefault:"true"`
}
