package modules

import (
	"context"
	"fmt"
	mathrand "math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type HelpModule struct {
	sender    *telegram.MessageSender
	userMap   *types.SafeUserMap
	startTime time.Time
}

func NewHelpModule(sender *telegram.MessageSender, userMap *types.SafeUserMap, startTime time.Time) *HelpModule {
	return &HelpModule{
		sender:    sender,
		userMap:   userMap,
		startTime: startTime,
	}
}

func (m *HelpModule) getStartText() string {
	what := "direct links, torrents, and Telegram files"
	if config.Get().DisableTorrents {
		what = "direct links, Mega, and Telegram files"
	}
	return "<i>This bot downloads and leeches " + what + " straight back to Telegram.</i>\n\n" +
		"<i>Type /help to see the available commands.</i>"
}

func (m *HelpModule) getStartButtons() *telegram.ButtonMaker {
	// like the Python bot: channel and group, then Help under them
	btn := telegram.NewButtonMaker()
	btn.UButtonStyled("Updates 🗞️", "https://t.me/TJ_Bots", "primary")
	btn.UButtonStyled("Group 🧲", "https://t.me/Tj_Leech_Group", "primary")
	btn.Row()
	btn.IButtonStyled("Help 📖", "start_help", "primary")
	btn.Row()
	return btn
}

func (m *HelpModule) getHelpGuideText() string {
	cfg := config.Get()
	sfx := cfg.CmdSuffix
	return fmt.Sprintf("⌬ <b><u>%s :</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Leech & Mirror:</b>\n"+
		"┠ <code>/leech%s [link]</code> — "+leechSources()+"\n"+
		"┠ <code>/zipleech%s [link]</code> — Zip contents before uploading\n"+
		"┠ <code>/unzipleech%s [link]</code> — Extract zip/rar/7z archives\n"+
		"┠ <code>/mirror%s [link]</code> — Upload to GoFile, VikingFile, PixelDrain, BuzzHeavier, DevUploads or StreamTape (<code>-up gofile</code>)\n"+
		"<b>╰</b> <code>/ytdl%s [link]</code> — Download video/audio via yt-dlp</blockquote>\n\n"+
		"<blockquote><b>╭</b> <b>User Configuration:</b>\n"+
		"┠ <code>/us%s</code> — User settings (Prefix, Suffix, Caption, AutoRename)\n"+
		"┠ <code>/ar%s [template]</code> — Quick AutoRename template set/clear\n"+
		"┠ <code>/setthumb%s</code> (reply) — Set custom leech thumbnail\n"+
		"┠ <code>/status%s</code> — Live download & upload speed and progress\n"+
		"<b>╰</b> <code>/history</code> — Your last 10 finished tasks</blockquote>\n\n"+
		"<blockquote><b>╭</b> <b>Admin & Control:</b>\n"+
		"┠ <code>/bs%s</code> — Bot Settings (Config vars, Aria2c, Private files)\n"+
		"┠ <code>/cancel%s [GID]</code> — Cancel specific task\n"+
		"┠ <code>/a</code> <code>/ua</code> — Authorize / unauthorize a user or this chat (reply or ID)\n"+
		"┠ <code>/addsudo</code> <code>/rmsudo</code> — Add / remove a Sudo user (owner)\n"+
		"┠ <code>/bl</code> <code>/rbl</code> — Blacklist / unblacklist a user\n"+
		"┠ <code>/users</code> <code>/authlist</code> <code>/sudolist</code> — Lists of users\n"+
		"<b>╰</b> <code>/cancelall%s</code> — Cancel all your active tasks</blockquote>",
		telegram.ToSmallCaps("Commands Guide"),
		sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx)
}

func (m *HelpModule) HandleStart(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.GetOrCreate(msg.UserID)
	u.StartedInPM = true
	if msg.SenderName != "" {
		u.Name = msg.SenderName
	}
	_ = ext.DB.SaveUser(u)

	tokens := strings.Fields(msg.Text)
	if len(tokens) > 1 {
		tokenArg := tokens[1]
		if tokenArg == "pm" { // from 📥 View in Bot PM: only opens the chat, no start message
			_ = m.sender.DeleteMessage(ctx, peer, []int{msg.MessageID})
			return
		}
		if HandleStoreStart(ctx, m.sender, peer, msg, tokenArg) {
			return
		}
		if ok, reply := ext.VerifyToken(tokenArg, msg.UserID, m.userMap); ok {
			_, _ = m.sender.SendMessage(ctx, peer, reply, msg.MessageID, nil)
			return
		}
	}

	startText := m.getStartText()
	sendWithImage(ctx, m.sender, peer, startText, msg.MessageID, m.getStartButtons(), 2)
}

func (m *HelpModule) HandleHelp(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	helpText := m.getHelpGuideText()
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", msg.UserID), "danger")
	btn.Row()
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, helpText, msg.MessageID, btn, 1)
}

func (m *HelpModule) HandlePing(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	start := time.Now()
	resID, err := m.sender.SendMessage(ctx, peer, "<i>Pinging...</i>", msg.MessageID, nil)
	if err == nil && resID > 0 {
		diff := time.Since(start).Milliseconds()
		_ = m.sender.EditMessage(ctx, peer, resID, fmt.Sprintf("⌬ <b>Pong!</b> <code>%d ms</code>", diff), telegram.CloseMarkup(msg.UserID))
	}
}

func (m *HelpModule) getStatsView(key string, targetUID int64) (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var msg string

	if key == "home" {
		btn.IButton("Bot Stats", fmt.Sprintf("stats %d stbot", targetUID))
		btn.IButton("OS Stats", fmt.Sprintf("stats %d stsys", targetUID))
		btn.Row()
		btn.IButton("Repo Stats", fmt.Sprintf("stats %d strepo", targetUID))
		btn.IButton("Bot Limits", fmt.Sprintf("stats %d botlimits", targetUID))
		btn.Row()
		btn.IButtonStyled("❌", fmt.Sprintf("stats %d close", targetUID), "danger")
		msg = "⌬ <b><i>Bot & OS Statistics!</i></b>"
	} else {
		btn.IButtonStyled("◀️", fmt.Sprintf("stats %d home", targetUID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("stats %d close", targetUID), "danger")

		switch key {
		case "stbot":
			msg = ext.RenderBotStats(m.startTime)
		case "stsys":
			msg = ext.RenderSysStats()
		case "strepo":
			msg = ext.RenderRepoStats()
		case "botlimits":
			msg = ext.RenderBotLimits()
		default:
			msg = "⌬ <b><i>Bot & OS Statistics!</i></b>"
		}
	}

	return msg, btn
}

func (m *HelpModule) HandleStats(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	text, btn := m.getStatsView("home", msg.UserID)
	sendWithImage(ctx, m.sender, peer, text, msg.MessageID, btn, 2)
}

func (m *HelpModule) HandleStatsCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	if data == "start_help" {
		helpText := m.getHelpGuideText()
		helpBtn := telegram.NewButtonMaker()
		helpBtn.IButtonStyled("◀️", "start_back", "primary")
		helpBtn.IButtonStyled("❌", fmt.Sprintf("start_close %d", fromUID), "danger")
		helpBtn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, helpText, helpBtn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	if data == "start_back" {
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, m.getStartText(), m.getStartButtons(), 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	if data == "start_botpm" { // 📥 View in Bot PM: opens the bot without the "open this link?" question
		_ = m.sender.AnswerCallbackURL(ctx, queryID, fmt.Sprintf("https://t.me/%s?start=pm", telegram.CurrentBotUsername))
		return
	}
	if data == "start_close" || strings.HasPrefix(data, "start_close ") {
		if owner := strings.TrimSpace(strings.TrimPrefix(data, "start_close")); owner != "" && owner != fmt.Sprintf("%d", fromUID) {
			_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
			return
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
		m.sender.DeleteWithCommand(ctx, peer, msgID)
		return
	}

	parts := strings.Fields(data)
	if len(parts) < 3 {
		return
	}

	var targetUID int64
	var key string

	if parts[0] == "wzmlx" {
		targetUID, _ = strconv.ParseInt(parts[1], 10, 64)
		if parts[2] == "close" {
			key = "close"
		} else if len(parts) >= 4 && parts[2] == "stats" {
			key = parts[3]
		}
	} else if parts[0] == "stats" {
		targetUID, _ = strconv.ParseInt(parts[1], 10, 64)
		key = parts[2]
	}

	if targetUID != 0 && targetUID != fromUID && !telegram.IsOwner(fromUID) {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}

	if key == "close" {
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed!", false)
		m.sender.DeleteWithCommand(ctx, peer, msgID)
		return
	}

	text, btn := m.getStatsView(key, targetUID)
	_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
	_ = m.sender.AnswerCallback(ctx, queryID, "", false)
}

func (m *HelpModule) HandleLog(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can view logs!", msg.MessageID, nil)
		return
	}
	if parts := strings.Fields(msg.Text); len(parts) > 1 && strings.EqualFold(parts[1], "clear") {
		ext.LogBuffer.Reset()
		_, _ = m.sender.SendMessage(ctx, peer, "✓ <b>Log cleared.</b> /log now shows only what happens from here.", msg.MessageID, telegram.CloseMarkup(msg.UserID))
		return
	}
	data := ext.LogBuffer.Bytes()
	if len(data) == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "⌬ <b>Logs:</b> nothing logged yet.", msg.MessageID, nil)
		return
	}
	if _, err := m.sender.SendDocumentBytes(ctx, peer, "log.txt", data, "⌬ <b>Latest logs</b>\n<i>Clear with</i> <code>/log clear</code>", msg.MessageID, telegram.CloseMarkup(msg.UserID)); err != nil {
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Could not send the log:</b> <code>%s</code>", telegram.EscapeHTML(err.Error())), msg.MessageID, nil)
	}
}

func (m *HelpModule) HandleRestart(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner can restart the bot!", msg.MessageID, nil)
		return
	}

	statusMsgID, _ := m.sender.SendMessage(ctx, peer, "• <i>Checking for updates and restarting...</i>", msg.MessageID, nil)

	onStatus := func(text string) {
		if statusMsgID > 0 {
			_ = m.sender.EditMessage(ctx, peer, statusMsgID, text, nil)
		} else {
			_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, nil)
		}
	}

	recordRestartMsg := func() error {
		if statusMsgID > 0 {
			return os.WriteFile(".restartmsg", []byte(fmt.Sprintf("%d %d", msg.ChatID, statusMsgID)), 0644)
		}
		return nil
	}

	go func() {
		_ = ext.UpdateAndRestart(context.Background(), onStatus, recordRestartMsg)
	}()
}

func leechSources() string {
	if config.Get().DisableTorrents {
		return "Direct, Mega, Google Drive, TG media"
	}
	return "Direct, Magnet, Torrent, Mega, TG media"
}

// sendWithImage: like the Python bot (IMAGES): with images set, a random one with the text as
// its caption; without them (default) or when it fails, the text alone.
func sendWithImage(ctx context.Context, sender *telegram.MessageSender, peer tg.InputPeerClass, text string, replyTo int, btn *telegram.ButtonMaker, cols int) {
	if imgs := strings.Fields(config.Get().Images); len(imgs) > 0 {
		if plain, _ := telegram.ParseFormatting(text); len([]rune(plain)) <= 1000 {
			if id, err := sender.SendPhotoFromURLWithButtons(ctx, peer, imgs[mathrand.Intn(len(imgs))], text, replyTo, btn, cols); err == nil && id > 0 {
				return
			}
		}
	}
	_, _ = sender.SendMessageWithButtons(ctx, peer, text, replyTo, btn, cols)
}
