# Leech Bot (Go)

A Telegram leech bot written in Go. It downloads direct links, Mega, Google Drive,
yt-dlp sites and Telegram files, and uploads them back to Telegram (or to a DDL host with `/mirror`).
For our own use only.

---

## Commands

| Command | What it does |
|---|---|
| `/leech` `/l` | Download and upload to Telegram (`/l` alone shows all flags) |
| `/zipleech` `/unzipleech` | Same, zip / extract first |
| `/mirror` `/m` | Upload to GoFile, PixelDrain, BuzzHeavier, StreamTape and more |
| `/ytdl` `/y` | yt-dlp with quality buttons |
| `/status` `/s` | Running tasks |
| `/cancel_GID` · `/cancelall` | Stop a task / all tasks |
| `/us` | User settings |
| `/bs` | Bot settings (owner) |
| `/mediainfo` · `/history` · `/stats` · `/ping` · `/log` · `/help` | Tools |

With `CMD_SUFFIX` (e.g. `1`) every command gets the suffix: `/l1`, `/s1`, `/cancel1_GID` …

---

## Deploy on Heroku

Heroku builds the `Dockerfile` itself (container stack), nothing is built on your PC.

### 1. Get the code
```bash
git clone https://github.com/Tj-Bots/azml-go.git
cd azml-go
```

### 2. Create the app
```bash
heroku login
heroku apps:create APP_NAME --stack=container
# in a team:  heroku apps:create APP_NAME --stack=container --team TEAM_NAME
heroku git:remote -a APP_NAME
```

### 3. Variables
```bash
heroku config:set -a APP_NAME \
  BOT_TOKEN="123:ABC" \
  OWNER_ID="123456789" \
  TELEGRAM_API="12345" \
  TELEGRAM_HASH="abcdef..." \
  DATABASE_URL="mongodb+srv://..." \
  LEECH_LOG_ID="-100..." \
  BASE_URL="https://APP_NAME-xxxx.herokuapp.com"
```
`BASE_URL` = the `Web URL` from `heroku info -a APP_NAME` (needed for file selection with `-s`).
Everything else is set later from `/bs` and saved in the database.

### 4. Deploy / update
```bash
git pull
git push heroku main -f
heroku logs --tail -a APP_NAME
```
Always update with `git push`. `/restart` does not rebuild on Heroku.

---

## A second bot

1. Create a new bot in @BotFather (new `BOT_TOKEN`).
2. Update the first bot once with this version (it keeps its database).
3. Create a second app and give it its own git remote:
   ```bash
   heroku apps:create APP_NAME_2 --stack=container
   heroku git:remote -a APP_NAME_2 -r heroku2
   ```
4. Same variables as above, with the new token, its own `BASE_URL` and a suffix:
   ```bash
   heroku config:set -a APP_NAME_2 BOT_TOKEN="NEW:TOKEN" CMD_SUFFIX="2" ...
   ```
5. Deploy it:
   ```bash
   git push heroku2 main -f
   ```

Both bots can use the same `DATABASE_URL`: every bot gets its own database automatically
(`DATABASE_NAME` sets one by hand). In a shared group, use `/l` for one bot and `/l2` for the other.

---

## VPS (Docker)
```bash
# create config.env with the variables above (one KEY=value per line), then:
docker compose up -d --build
```
