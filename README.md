<h1><img src="assets/logo.png" alt="" width="56" height="56" align="absmiddle"> Immich Telegram Uploader</h1>

A Telegram bot for Immich owners who want friends or family to send photos and videos without creating Immich accounts. Files go into your Immich library, either in one album or in separate albums named `@username uploads`.

Each upload gets a description with the sender's Telegram username. The included Docker Compose setup runs a local Telegram Bot API server so the bot can receive files larger than 20 MB. That server downloads media to a shared volume; the bot reads it and streams it to Immich.

## Prepare Immich

1. If you don't have Immich running, follow the [official Docker Compose guide](https://docs.immich.app/install/docker-compose/): download its Compose and environment files into a separate directory, set storage paths and a database password, then run `docker compose up -d` there.
2. Sign in to the Immich account that should own the uploads. In the web interface's user settings, [create an API key](https://docs.immich.app/features/command-line-interface/#obtain-the-api-key). Give it permission to upload and update assets, add assets to albums, and, for separate albums, list and create albums.
3. Choose your album mode: let the bot create an album for each sender, or create one destination album in Immich and copy its ID from the album page's URL.

## Deploy the bot

Requires Docker with the Compose plugin and disk space for downloaded media. Compose runs the bot and the [community-maintained aiogram image](https://github.com/aiogram/telegram-bot-api) of Telegram's Bot API server.

1. Create a bot through [@BotFather](https://t.me/BotFather) and save its token. Get your numeric Telegram user ID through `@userinfobot`.
   Obtain an API ID and hash through [Telegram's API development tools](https://core.telegram.org/api/obtaining_api_id). These credentials are separate from the bot token and are required by the local API server.
2. Clone this repository and copy the example configuration:

   ```sh
   git clone https://github.com/0dd431c8/immich-telegram-uploader.git
   cd immich-telegram-uploader
   cp .env.example .env
   ```

3. Edit `.env` with your values. The example configuration already selects the local API server; replace its credential placeholders:

   ```dotenv
   TELEGRAM_BOT_TOKEN=your-bot-token
   TELEGRAM_BOT_ADMIN_ID=123456789
   TELEGRAM_API_ID=123456
   TELEGRAM_API_HASH=your-telegram-api-hash
   TELEGRAM_BOT_API_URL=http://telegram-bot-api:8081
   TELEGRAM_LOCAL_MODE=true
   IMMICH_API_URL=http://192.168.1.10:2283/api
   IMMICH_TOKEN=your-immich-api-key
   IMMICH_PER_CLIENT_ALBUM=true
   ```

   Use an Immich address reachable from the bot container, including `/api`. Inside Docker, `localhost` points to the bot itself. For one destination album, set `IMMICH_PER_CLIENT_ALBUM=false` and add `IMMICH_ALBUM_ID=your-album-id`. Keep `.env` private.

4. Before the first local start, follow [Switch from the hosted Bot API](#switch-from-the-hosted-bot-api) below to log the bot out of Telegram's hosted API. Then validate, build, and start:

   ```sh
   docker compose config --quiet
   docker compose up -d --build
   docker compose logs -f telegram-bot-api immich-telegram-uploader
   ```

## Switch from the hosted Bot API

Stop any running instance of this bot that connects to `api.telegram.org`. For an existing Compose deployment:

```sh
docker compose stop immich-telegram-uploader
```

Call Telegram's hosted [`logOut` method](https://core.telegram.org/bots/api#logout) once before connecting locally. In a shell where `TELEGRAM_BOT_TOKEN` is exported with your bot's token:

```sh
curl --silent --show-error --request POST \
  "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/logOut"
```

Check for `{"ok":true,"result":true}` before starting the local deployment. A successful logout permits an immediate connection to the local server; reconnecting to the hosted API is blocked for 10 minutes. Do not call hosted `logOut` on every restart.

## Telegram API settings and storage

| Setting | Purpose |
| --- | --- |
| `TELEGRAM_API_ID`, `TELEGRAM_API_HASH` | Required by Compose for the local API server; not read by the Go bot. |
| `TELEGRAM_BOT_API_URL` | Optional API base URL, without `/bot` or the token. Unset or empty uses Telegram's hosted API. |
| `TELEGRAM_LOCAL_MODE` | Defaults to `false` when omitted; `.env.example` sets it to `true`. Reads the absolute file paths returned by the local API server. Requires `TELEGRAM_BOT_API_URL`. |

Compose sets `TELEGRAM_LOCAL=1` on the API server and uses `TELEGRAM_LOCAL_MODE=true` from the bot's `.env`. These are separate settings for the two services. Both must be enabled for local-file uploads.

The `telegram-bot-api-data` volume is mounted at `/var/lib/telegram-bot-api` in both containers. The server writes there; the bot mounts it read-only and joins group `101` for access. Preserve the matching mount paths and ensure files and parent directories grant the bot read and traversal access. The API port is available inside the Compose network and is not published on the host.

Telegram's [local server removes the hosted API's download size limit](https://github.com/tdlib/telegram-bot-api#usage). Media uses space on the server's volume, and the bot does not delete those files after upload. Monitor available disk space. Upload progress tracks reading the local file into Immich after the API server finishes downloading it.

If startup reports a `getMe` timeout, check `docker compose logs telegram-bot-api` and the server's connection to Telegram. The bot allows one minute for initialization and 30 minutes per API HTTP request when a custom API URL is configured. `depends_on` orders container startup but does not wait for a working Telegram session.

## Use it

Open a private chat with your bot, send `/start`, then send photos or videos **as files/documents**. Senders need a Telegram username. Regular photo/video messages are not supported. While uploads are enabled, other messages handled by the upload handler receive a reminder to send attachments as files.

Uploads are enabled at startup. In your private chat, the configured admin can send `/disable`, `/enable`, or `/enable 60` to accept uploads for 60 seconds. Timed durations support 1 to 4,294,967,295 seconds; `/enable` or `/enable 0` enables uploads indefinitely. Restarting the bot enables uploads again.

Anyone with a Telegram username who can message the bot can upload while uploads are enabled. The admin ID only controls these commands; it is not an uploader allowlist.

## Run without Docker

Install Go 1.27.2, configure `.env`, and run `go run .`.

For Telegram's hosted API, leave `TELEGRAM_BOT_API_URL` empty and set `TELEGRAM_LOCAL_MODE=false`. Media streams over HTTP without a local media file, but Telegram limits [file downloads to 20 MB](https://core.telegram.org/bots/faq#how-do-i-download-files). The API ID and hash are not needed for this mode.

For a separately hosted local API server, set `TELEGRAM_BOT_API_URL` to its reachable address and enable `TELEGRAM_LOCAL_MODE`. The Go process must have read access to the same absolute paths that the API server returns. The Compose hostname `telegram-bot-api` is only available inside its Docker network.

## License

[MIT](LICENSE)
