package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	mathrand "math/rand"
	"mime/multipart"
	"net/http"
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
		"<b>╰</b> <code>"+telegram.Cmd("history")+"</code> — Your last 10 finished tasks</blockquote>\n\n"+
		"<blockquote><b>╭</b> <b>Admin & Control:</b>\n"+
		"┠ <code>/bs%s</code> — Bot Settings (Config vars, Aria2c, Private files)\n"+
		"┠ <code>/cancel%s [GID]</code> — Cancel specific task\n"+
		"┠ <code>"+telegram.Cmd("a")+"</code> <code>"+telegram.Cmd("ua")+"</code> — Authorize / unauthorize a user or this chat (reply or ID)\n"+
		"┠ <code>"+telegram.Cmd("addsudo")+"</code> <code>"+telegram.Cmd("rmsudo")+"</code> — Add / remove a Sudo user (owner)\n"+
		"┠ <code>"+telegram.Cmd("bl")+"</code> <code>"+telegram.Cmd("rbl")+"</code> — Blacklist / unblacklist a user\n"+
		"┠ <code>"+telegram.Cmd("users")+"</code> <code>"+telegram.Cmd("authlist")+"</code> <code>"+telegram.Cmd("sudolist")+"</code> — Lists of users\n"+
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
	if data == "start_botpm" || strings.HasPrefix(data, "start_botpm ") {
		// 📥 View in Bot PM: opens the bot without the "open this link?" question; only for the
		// user of the task (his files are in his PM), like the Python bot
		if owner := strings.TrimSpace(strings.TrimPrefix(data, "start_botpm")); owner != "" && owner != fmt.Sprintf("%d", fromUID) {
			_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
			return
		}
		_ = m.sender.AnswerCallbackURL(ctx, queryID, fmt.Sprintf("https://t.me/%s?start=pm", telegram.CurrentBotUsername))
		return
	}
	if strings.HasPrefix(data, "start_logdisp ") || strings.HasPrefix(data, "start_logpaste ") {
		if !telegram.IsSudo(fromUID, m.userMap) || !strings.HasSuffix(data, fmt.Sprintf(" %d", fromUID)) {
			_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
			return
		}
		m.logButton(ctx, peer, queryID, msgID, fromUID, strings.HasPrefix(data, "start_logdisp "))
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
	// like the Python bot: the file with Log Display and Web Paste
	btn := telegram.NewButtonMaker()
	btn.IButton("Log Display", fmt.Sprintf("start_logdisp %d", msg.UserID))
	btn.Row()
	btn.IButton("Web Paste", fmt.Sprintf("start_logpaste %d", msg.UserID))
	btn.Row()
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", msg.UserID), "danger")
	btn.Row()
	if _, err := m.sender.SendDocumentBytes(ctx, peer, "log.txt", data, logCaption(), msg.MessageID, btn.BuildMenu(1)); err != nil {
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

func logCaption() string {
	return "⌬ <b>Latest logs</b>\n<i>Clear with</i> <code>" + telegram.Cmd("log") + " clear</code>"
}

// pasteAPI: the TJ Files paste of the Python bot (web_paste).
const pasteAPI = "https://tjfiles.tjbots.chatgpt.site/api/"

// logButton: Log Display (the last lines as a message) or Web Paste (the whole log as a link).
func (m *HelpModule) logButton(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, uid int64, display bool) {
	data := ext.LogBuffer.Bytes()
	if display {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		var out []string
		size := 0
		for i := len(lines) - 1; i >= 0 && size <= 3500; i-- {
			out = append([]string{lines[i]}, out...)
			size += len(lines[i]) + 1
		}
		text := fmt.Sprintf("<b>Showing Last %d Lines from log.txt:</b>\n\n<pre>%s</pre>", len(out), telegram.EscapeHTML(strings.Join(out, "\n")))
		_, _ = m.sender.SendMessage(ctx, peer, text, msgID, telegram.CloseMarkup(uid))
		return
	}
	_ = m.sender.AnswerCallback(ctx, queryID, "Uploading the log...", false)
	link := webPaste(ctx, string(data))
	btn := telegram.NewButtonMaker()
	btn.IButton("Log Display", fmt.Sprintf("start_logdisp %d", uid))
	btn.Row()
	if link == "" {
		_, _ = m.sender.SendMessage(ctx, peer, "× <i>Web Paste failed, see the log.</i>", msgID, telegram.CloseMarkup(uid))
		btn.IButton("Web Paste", fmt.Sprintf("start_logpaste %d", uid))
	} else {
		btn.UButton("📨 Web Paste", link)
	}
	btn.Row()
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", uid), "danger")
	btn.Row()
	_ = m.sender.EditMessage(ctx, peer, msgID, logCaption(), btn.BuildMenu(1))
}

// webPaste: the text to the paste site; the newest part when it is too big (like the Python bot).
func webPaste(ctx context.Context, text string) string {
	for _, limit := range []int{0, 1_000_000, 200_000} {
		content := text
		if limit > 0 && len(content) > limit {
			content = content[len(content)-limit:]
		}
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("content", content)
		_ = w.Close()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, pasteAPI, &body)
		if err != nil {
			return ""
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err == nil {
			var res struct {
				Payload struct {
					ID string `json:"id"`
				} `json:"payload"`
				Error string `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&res)
			resp.Body.Close()
			if (resp.StatusCode == 200 || resp.StatusCode == 201) && res.Payload.ID != "" {
				return strings.TrimSuffix(pasteAPI, "api/") + res.Payload.ID
			}
			log.Printf("[Web Paste] failed: %d %s", resp.StatusCode, res.Error)
		} else {
			log.Printf("[Web Paste] failed: %v", err)
		}
		if len(content) <= 200_000 {
			break
		}
	}
	return ""
}
