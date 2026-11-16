package modules

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/helper/uploader"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// Media Store (MEDIA_STORE): t.me/<bot>?start=st<id> gives the file (from the dump / log channel)
// to whoever opens it.

func storeLink(chatID int64, msgID int) string {
	if chatID >= 0 || msgID <= 0 || telegram.CurrentBotUsername == "" {
		return ""
	}
	b := make([]byte, 12)
	binary.BigEndian.PutUint64(b, uint64(chatID))
	binary.BigEndian.PutUint32(b[8:], uint32(msgID))
	return fmt.Sprintf("https://t.me/%s?start=st%s", telegram.CurrentBotUsername, base64.RawURLEncoding.EncodeToString(b))
}

func parseStoreArg(arg string) (int64, int, bool) {
	if !strings.HasPrefix(arg, "st") {
		return 0, 0, false
	}
	b, err := base64.RawURLEncoding.DecodeString(arg[2:])
	if err != nil || len(b) != 12 {
		return 0, 0, false
	}
	return int64(binary.BigEndian.Uint64(b)), int(binary.BigEndian.Uint32(b[8:])), true
}

// HandleStoreStart sends the stored file; true when the /start was a store link.
func HandleStoreStart(ctx context.Context, sender *telegram.MessageSender, peer tg.InputPeerClass, msg *types.MessageContext, arg string) bool {
	chatID, msgID, ok := parseStoreArg(arg)
	if !ok {
		return false
	}
	cfg := config.Get()
	if chatID != cfg.DumpChatID && chatID != cfg.LeechLogID { // only files of the bot's own channels
		_, _ = sender.SendMessage(ctx, peer, "<i>This link is not valid.</i>", msg.MessageID, nil)
		return true
	}
	if !uploader.CopyMessage(msg.UserID, chatID, msgID, "") {
		_, _ = sender.SendMessage(ctx, peer, "<i>The file is not available anymore.</i>", msg.MessageID, nil)
	}
	return true
}
