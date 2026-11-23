package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
		"<i>Type " + telegram.Cmd("help") + " to see the available commands.</i>"
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

// The help guide like the Python bot: a menu (Basic / Users / Mics / Owner & Sudos), each page
// lists the commands of this bot (with its CMD_SUFFIX).
const helpHeader = "㊂ <b><i>Help Guide Menu!</i></b>\n\n<b>NOTE: <i>Send a command alone to see all its options.</i></b>"

func helpMenu(uid int64, back bool) *telegram.ButtonMaker {
	btn := telegram.NewButtonMaker()
	btn.IButton("Basic", fmt.Sprintf("start_hg %d basic", uid))
	btn.IButton("Users", fmt.Sprintf("start_hg %d users", uid))
	btn.Row()
	btn.IButton("Mics", fmt.Sprintf("start_hg %d miscs", uid))
	btn.IButton("Owner & Sudos", fmt.Sprintf("start_hg %d admin", uid))
	btn.Row()
	if back {
		btn.IButtonStyled("◀️", "start_back", "primary")
	}
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", uid), "danger")
	btn.Row()
	return btn
}

// helpPage: the commands of one page; "" for an unknown page.
func helpPage(page string) string {
	c := func(names ...string) string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = telegram.Cmd(n)
		}
		return strings.Join(out, " or ")
	}
	switch page {
	case "basic":
		return "⌬ <b><i>Bᴀsɪᴄ Cᴏᴍᴍᴀɴᴅs!</i></b>\n\n<blockquote>" +
			"<b>Use Leech commands for upload to Telegram:</b>\n" +
			"<b>╭</b> " + c("leech", "l") + ": " + leechSources() + " to Telegram.\n" +
			"┠ " + c("zipleech", "zl") + ": Zip the files before the upload.\n" +
			"<b>╰</b> " + c("unzipleech", "uzl") + ": Extract zip / rar / 7z before the upload.\n\n" +
			"<b>Use Mirror commands to upload to the cloud:</b>\n" +
			"<b>╰</b> " + c("mirror", "m") + ": Google Drive (<code>-up gd</code>), RClone (<code>-up rc</code>) or the DDL servers (GoFile, PixelDrain ...).\n\n" +
			"<b>Use yt-dlp commands for YouTube or any supported sites:</b>\n" +
			"<b>╭</b> " + c("ytdl", "y") + ": Download with quality buttons.\n" +
			"<b>╰</b> " + c("ytdlzip", "yz") + ": The same, zipped.\n\n" +
			"<b>Cancel Tasks:</b>\n" +
			"<b>╭</b> " + c("cancel", "c") + ": Cancel a task by its gid or reply.\n" +
			"<b>╰</b> " + c("cancelall", "ca") + ": Cancel all your tasks.</blockquote>"
	case "users":
		return "⌬ <b><i>Usᴇʀs Cᴏᴍᴍᴀɴᴅs!</i></b>\n\n<blockquote>" +
			"<b>Bot Settings:</b>\n" +
			"<b>╭</b> " + c("us") + ": Open User Settings (PM also).\n" +
			"┠ " + c("t") + ": Reply to a photo to set it as your thumbnail.\n" +
			"┠ " + c("ar") + " [template]: Set / clear your AutoRename template.\n" +
			"<b>╰</b> " + c("cover") + ": Reply to a video to set your thumbnail / TMDB poster as its cover.\n\n" +
			"<b>Bot Stats:</b>\n" +
			"<b>╭</b> " + c("status", "s") + ": All the active tasks. Add <code>me</code> for yours only.\n" +
			"┠ " + c("history") + ": Your last 10 finished tasks with their links.\n" +
			"┠ " + c("stats", "st") + ": Server stats.\n" +
			"<b>╰</b> " + c("ping") + ": How long it takes to ping the bot.\n\n" +
			"<b>Premium:</b>\n" +
			"<b>╭</b> " + c("premium") + ": Your premium status (PM also).\n" +
			"<b>╰</b> " + c("dump") + ": A join link of the Dump channel (in PM).</blockquote>"
	case "miscs":
		return "⌬ <b><i>Mɪsᴄᴇʟʟᴀɴᴇᴏᴜs Cᴏᴍᴍᴀɴᴅs!</i></b>\n\n<blockquote>" +
			"<b>Extras:</b>\n" +
			"<b>╭</b> " + c("speedtest", "sp") + ": Check the speed of the server.\n" +
			"<b>╰</b> " + c("mediainfo", "mi") + " [url/media]: MediaInfo of a file or a download link.\n\n" +
			"<b>Movie/TV Shows Search:</b>\n" +
			"<b>╭</b> " + c("p", "poster") + " [name]: Posters & backdrops of a movie / show (or reply to a file).\n" +
			"┠ " + c("imdb") + ": Search in IMDB.\n" +
			"<b>╰</b> " + c("anime", "al") + ": Search for anime in AniList.</blockquote>"
	case "admin":
		return "⌬ <b><i>Oᴡɴᴇʀ ᴏʀ Sᴜᴅᴏs Cᴏᴍᴍᴀɴᴅs!</i></b>\n\n<blockquote>" +
			"<b>Bot Settings:</b>\n" +
			"<b>╭</b> " + c("bs") + ": Open Bot Settings.\n" +
			"<b>╰</b> " + c("users") + ": User stats info.\n\n" +
			"<b>Authentication:</b>\n" +
			"<b>╭</b> " + c("authorize", "a") + ": Authorize a chat or a user.\n" +
			"┠ " + c("unauthorize", "ua") + ": Unauthorize a chat or a user.\n" +
			"┠ " + c("authlist") + " / " + c("sudolist") + " / " + c("blacklisted") + ": The lists of users.\n" +
			"┠ " + c("addsudo") + " / " + c("rmsudo") + ": Add / remove a sudo user (Only Owner).\n" +
			"┠ " + c("addpremium", "ap") + " / " + c("rmpremium", "rp") + ": Add / remove a premium user.\n" +
			"<b>╰</b> " + c("blacklist", "bl") + " / " + c("rmblacklist", "rbl") + ": Blacklist / unblacklist a user.\n\n" +
			"<b>Bot Stats:</b>\n" +
			"<b>╰</b> " + c("broadcast", "bc") + " [reply]: Broadcast to the users who started the bot.\n\n" +
			"<b>Cancel Tasks:</b>\n" +
			"<b>╰</b> /cancelallbot: Cancel all the tasks of all the bots.\n\n" +
			"<b>Maintenance:</b>\n" +
			"<b>╭</b> " + c("restart", "r") + ": Restart and update the bot.\n" +
			"┠ /restartall: Restart and update all the bots.\n" +
			"┠ /statusall: The status of all the bots.\n" +
			"┠ " + c("log") + ": The log of the bot.\n" +
			"<b>╰</b> " + c("shell") + ": Run shell commands (Only Owner).</blockquote>"
	}
	return ""
}

func (m *HelpModule) HandleHelp(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, helpHeader, msg.MessageID, helpMenu(msg.UserID, false), 2)
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
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, startText, msg.MessageID, m.getStartButtons(), 2)
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
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 2)
}

func (m *HelpModule) HandleStatsCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	if data == "start_help" { // the Help button of /start: the menu, ◀️ back to /start
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, helpHeader, helpMenu(fromUID, true), 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	if strings.HasPrefix(data, "start_hg ") { // "start_hg <uid> <page>" (home = the menu)
		parts := strings.Fields(data)
		if len(parts) < 3 || parts[1] != fmt.Sprintf("%d", fromUID) {
			_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
			return
		}
		page := parts[2]
		if page == "admin" && !telegram.IsSudo(fromUID, m.userMap) {
			_ = m.sender.AnswerCallback(ctx, queryID, "Not Sudo or Owner!", true)
			return
		}
		text := helpPage(page)
		if text == "" {
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, helpHeader, helpMenu(fromUID, false), 2)
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			return
		}
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("◀️", fmt.Sprintf("start_hg %d home", fromUID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", fromUID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
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
