package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"

	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// A backup file (azml_settings.json of /bs Export, azml_user_settings.json of /us Export) sent to
// the bot: it asks "Import?" at once, no need to open /bs or /us first.

type importOffer struct {
	uid    int64
	bot    bool // /bs settings (owner / sudo), else /us settings
	data   []byte
	fileID int // the file message (it may hold keys: deleted after the answer)
	at     time.Time
}

var (
	importMu     sync.Mutex
	importOffers = map[string]*importOffer{}
)

func (b *BotApp) offerImport(ctx context.Context, peer tg.InputPeerClass, fileMsgID int, uid int64, botFile bool, doc *tg.Document) {
	if botFile && !telegram.IsOwner(uid) && !telegram.IsSudo(uid, b.userMap) {
		_, _ = b.sender.SendMessage(ctx, peer, "<i>Only the owner or a sudo user can import bot settings.</i>", fileMsgID, nil)
		return
	}
	if (botFile && b.settingsImporter == nil) || (!botFile && b.userSettingsImporter == nil) {
		return
	}
	var buf bytes.Buffer
	if _, err := tdDownloader.NewDownloader().Download(b.rawAPI, doc.AsInputDocumentFileLocation("")).Stream(ctx, &buf); err != nil {
		_, _ = b.sender.SendMessage(ctx, peer, "× <i>Download failed: "+telegram.EscapeHTML(err.Error())+"</i>", fileMsgID, nil)
		return
	}
	var f struct {
		Kind     string                     `json:"kind"`
		Exported string                     `json:"exported"`
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &f); err != nil || f.Settings == nil {
		_, _ = b.sender.SendMessage(ctx, peer, "× <i>This is not a settings file of Export.</i>", fileMsgID, nil)
		return
	}
	importMu.Lock()
	for k, o := range importOffers { // 10 minutes to answer
		if time.Since(o.at) > 10*time.Minute {
			delete(importOffers, k)
		}
	}
	token := ext.RandomHex(4)
	importOffers[token] = &importOffer{uid: uid, bot: botFile, data: buf.Bytes(), fileID: fileMsgID, at: time.Now()}
	importMu.Unlock()

	what, where := "User Settings", "/us"
	if botFile {
		what, where = "Bot Settings", "/bs"
	}
	text := fmt.Sprintf("㊂ <b><u>Import %s :</u></b>\n\n<blockquote><b>╭</b> <b>Settings :</b> %d\n", what, len(f.Settings))
	if f.Exported != "" {
		text += "┠ <b>Exported :</b> " + telegram.EscapeHTML(f.Exported) + "\n"
	}
	text += "<b>╰</b> <b>Into :</b> " + where + " of this bot</blockquote>\n\n<i>Import this backup?</i>"
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("✓ Import", "impset "+token+" y", "success")
	btn.IButtonStyled("✘ Cancel", "impset "+token+" n", "danger")
	btn.Row()
	_, _ = b.sender.SendMessageWithButtons(ctx, peer, text, fileMsgID, btn, 2)
}

// handleImportOffer: "impset <token> y|n".
func (b *BotApp) handleImportOffer(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		_ = b.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	importMu.Lock()
	o := importOffers[parts[1]]
	if o != nil && o.uid == fromUID {
		delete(importOffers, parts[1])
	}
	importMu.Unlock()
	if o == nil {
		_ = b.sender.AnswerCallback(ctx, queryID, "This expired, send the file again.", true)
		_ = b.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	if o.uid != fromUID {
		_ = b.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	_ = b.sender.DeleteMessage(ctx, peer, []int{o.fileID}) // the file may hold keys
	if parts[2] != "y" {
		_ = b.sender.AnswerCallback(ctx, queryID, "Cancelled", false)
		_ = b.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	_ = b.sender.AnswerCallback(ctx, queryID, "Importing...", false)
	if o.bot {
		b.settingsImporter(ctx, peer, msgID, o.data)
	} else {
		b.userSettingsImporter(ctx, peer, msgID, o.uid, o.data)
	}
}
