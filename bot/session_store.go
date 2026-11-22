package bot

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"strings"

	"azml-go/helper/ext"

	"github.com/gotd/td/session"
)

// dbSession keeps the bot's Telegram session in the database (bot settings, "_" keys are never
// shown or exported). Without it every start logged in with the token again, and after a few
// restarts Telegram answered FLOOD_WAIT (15 minutes) and the bot could not start.
type dbSession struct {
	key string
}

func newDBSession(botToken string) *dbSession {
	id := strings.SplitN(strings.TrimSpace(botToken), ":", 2)[0]
	return &dbSession{key: "_TG_SESSION_" + id}
}

func (s *dbSession) LoadSession(ctx context.Context) ([]byte, error) {
	db := ext.GetDB()
	if db == nil {
		return nil, session.ErrNotFound
	}
	v, err := db.GetBotSetting(s.key)
	if err != nil || v == "" {
		return nil, session.ErrNotFound
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) == 0 {
		return nil, session.ErrNotFound
	}
	return b, nil
}

func (s *dbSession) StoreSession(ctx context.Context, data []byte) error {
	db := ext.GetDB()
	if db == nil {
		return nil
	}
	return db.SaveBotSetting(s.key, base64.StdEncoding.EncodeToString(data))
}

// private files kept in the database: a dyno restart starts with an empty disk
var privateFiles = []string{"token.pickle", "rclone.conf"}

// savePrivateFile: after token.pickle / rclone.conf is sent to the bot.
func savePrivateFile(name string) {
	db := ext.GetDB()
	if db == nil {
		return
	}
	if b, err := os.ReadFile(name); err == nil {
		_ = db.SaveBotSetting("_FILE_"+name, base64.StdEncoding.EncodeToString(b))
	}
}

// RestorePrivateFiles: at start, the files of the database that are not on disk.
func RestorePrivateFiles() {
	db := ext.GetDB()
	if db == nil {
		return
	}
	for _, name := range privateFiles {
		if _, err := os.Stat(name); err == nil {
			continue
		}
		v, _ := db.GetBotSetting("_FILE_" + name)
		if v == "" {
			continue
		}
		if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) > 0 {
			if os.WriteFile(name, b, 0o600) == nil {
				log.Printf("[DB] Restored %s from the database", name)
			}
		}
	}
}
