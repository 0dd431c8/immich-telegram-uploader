<h1><img src="assets/logo.png" alt="" width="56" height="56" align="absmiddle"> Immich Telegram upload</h1>

A Telegram bot for Immich owners who want friends or family to send photos and videos without creating Immich accounts. Files go into your Immich library, either in one album or in separate albums named `@username uploads`.

The bot streams media from Telegram to Immich without saving it locally. Each upload gets a description with the sender's Telegram username.

## Prepare Immich

1. If you don't have Immich running, follow the [official Docker Compose guide](https://docs.immich.app/install/docker-compose/): download its Compose and environment files into a separate directory, set storage paths and a database password, then run `docker compose up -d` there.
2. Sign in to the Immich account that should own the uploads. In the web interface's user settings, [create an API key](https://docs.immich.app/features/command-line-interface/#obtain-the-api-key). Give it permission to upload and update assets, add assets to albums, and, for separate albums, list and create albums.
3. Choose your album mode: let the bot create an album for each sender, or create one destination album in Immich and copy its ID from the album page's URL.

## Deploy the bot

Requires Docker with the Compose plugin.

1. Create a bot through [@BotFather](https://t.me/BotFather) and save its token. Get your numeric Telegram user ID through `@userinfobot`.
2. Clone this repository and copy the example configuration:

   ```sh
   git clone https://github.com/0dd431c8/immich-telegram-upload.git
   cd immich-telegram-upload
   cp .env.example .env
   ```

3. Edit `.env` with your values:

   ```dotenv
   TELEGRAM_BOT_TOKEN=your-bot-token
   TELEGRAM_BOT_ADMIN_ID=123456789
   IMMICH_API_URL=http://192.168.1.10:2283/api
   IMMICH_TOKEN=your-immich-api-key
   IMMICH_PER_CLIENT_ALBUM=true
   ```

   Use an Immich address reachable from the bot container, including `/api`. Inside Docker, `localhost` points to the bot itself. For one destination album, set `IMMICH_PER_CLIENT_ALBUM=false` and add `IMMICH_ALBUM_ID=your-album-id`. Keep `.env` private.

4. Build and start:

   ```sh
   docker compose up -d --build
   docker compose logs -f immich-telegram-upload
   ```

## Use it

Open a private chat with your bot, send `/start`, then send photos or videos **as files/documents**. Senders need a Telegram username. Regular photo/video messages are not supported, even with `ONLY_FILE_ATTACHMENT=false`. Telegram's hosted Bot API limits [file downloads to 20 MB](https://core.telegram.org/bots/faq#how-do-i-download-files).

Uploads are enabled at startup. In your private chat, the configured admin can send `/disable`, `/enable`, or `/enable 60` to accept uploads for 60 seconds (supported range: 1 to 255 seconds). Restarting the bot enables uploads again.

Anyone with a Telegram username who can message the bot can upload while uploads are enabled. The admin ID only controls these commands; it is not an uploader allowlist.

To run without Docker, install Go 1.27.2, configure `.env`, and run `go run .`.
