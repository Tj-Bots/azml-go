package modules

import (
	"context"
	"fmt"

	"azml-go/helper/telegram"
	"azml-go/helper/uploader"

	"github.com/gotd/td/tg"
)

// Save Message (SAVE_MSG) like the Python bot: whoever presses it gets a copy of the
// message in his PM, without the Save button.
const saveMsgData = "save"

func saveMsgButton(btn *telegram.ButtonMaker) {
	btn.IButton("📨 Save Message", saveMsgData)
	btn.Row()
}

// HandleSaveCallback copies the pressed message to the user's PM.
func (m *LeechModule) HandleSaveCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	chatID := telegram.PeerToChatID(peer)
	markup := ""
	if msg, err := telegram.FetchMessage(ctx, m.rawAPI, peer, msgID); err == nil && msg != nil {
		markup = telegram.KeyboardWithout(msg.ReplyMarkup, saveMsgData)
	}
	if uploader.CopyMessage(fromUID, chatID, msgID, markup) {
		_ = m.sender.AnswerCallback(ctx, queryID, "Message/Media Successfully Saved !", true)
		return
	}
	// the user never started the bot: Telegram opens the bot for him
	_ = m.sender.AnswerCallbackURL(ctx, queryID, fmt.Sprintf("https://t.me/%s?start=start", telegram.CurrentBotUsername))
}
